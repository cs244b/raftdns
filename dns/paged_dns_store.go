package main

import (
	"bytes"
	"container/list"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash"
	"github.com/coreos/etcd/snap"

	"github.com/miekg/dns"
)

// PageStoreMaxLen is max number of pages allowed in memory
// Set this to 2 for easy debugging validation of LRU policy.
const PageStoreMaxLen = 100

// PageSize is the expected size inside of a page.
// e.g. if PageSize = 1, there will be 2^63 pages
// Set this to 62 for easy debugging validation of LRU policy.
const PageSize = 32

// Get page number given a (domain) name. The corresponding entry should
// be on that single page.
func getPageNumber(name string) uint64 {
	return xxhash.Sum64String(name) >> PageSize
}

// PageStore is a storage unit of a page.
type PageStore struct {
	Key             uint64
	LastCommitIndex uint64 // Last commit applied to this page store. Saved to ensure idempotency
	Store           map[string]dnsRRTypeMap
	// The following is private and thus not serialized in JSON.
	lruElement *list.Element // element in LRU list for fast removal / reinsert
}

type pageStoreMap map[uint64]*PageStore

type pagedDNSStore struct {
	proposeC chan<- string // New entry proposals is sent to here
	mu       sync.RWMutex
	pages    pageStoreMap // ...Where actual entrys are stored
	// NOTICE: lruList elements are of type *pageStore
	lruList     list.List // List of least recently used pageStore
	snapshotter *snap.Snapshotter
	cache       map[string]cacheRRTypeMap // cache for non-authoritative records
	// XXX: optionally need a cache for glue records
	// for sending http Cache requests
	cluster []string
	id      int
}

func newPagedDNSStore(snapshotter *snap.Snapshotter, proposeC chan<- string, commitC <-chan *commitInfo, errorC <-chan error, cluster []string, id int) *pagedDNSStore {
	s := &pagedDNSStore{
		proposeC:    proposeC,
		pages:       make(map[uint64]*PageStore),
		snapshotter: snapshotter,
		cache:       make(map[string]cacheRRTypeMap),
		cluster:     cluster,
		id:          id,
	}

	maybeCreatePageDir() // Create pagedir if not exist

	// Replay historical commits to in-memory store
	s.readCommits(commitC, errorC, true)
	// Read more commits for incoming (commits that are caused by proposals)
	go s.readCommits(commitC, errorC, false)
	return s
}

func maybeCreatePageDir() {
	if _, err := os.Stat("./pagedir"); os.IsNotExist(err) {
		os.Mkdir("./pagedir", 0777)
	}
}

func readSerializedPage(pageNum uint64) []byte {
	maybeCreatePageDir()
	pageFilename := fmt.Sprintf("./pagedir/page_%d", pageNum)
	pageFile, err := os.OpenFile(pageFilename, os.O_RDWR|os.O_CREATE, 0777)
	if err != nil {
		return nil
	}
	data, err := ioutil.ReadAll(pageFile)
	if err != nil {
		return nil
	}
	return data
}

func writeSerializedPage(pageNum uint64, data []byte) error {
	maybeCreatePageDir()
	tempPageFilename := fmt.Sprintf("./pagedir/page_%d_temp", pageNum)
	pageFilename := fmt.Sprintf("./pagedir/page_%d", pageNum)
	// Write to temporary file first. Avoids corrupting good page on sudden shutdown
	err := ioutil.WriteFile(tempPageFilename, data, 0777)
	if err == nil {
		// If no error, do atomic-renaming like operation.
		// This is technically not atomic, but the risk window is way smaller than direct rewriting.
		os.Remove(pageFilename)
		os.Rename(tempPageFilename, pageFilename)
	} else { // Has error, remove temporary file.
		os.Remove(tempPageFilename)
	}
	return err
}

func (s *pagedDNSStore) readPageIn(pageNum uint64) {
	if _, ok := s.pages[pageNum]; ok {
		return // Already in map, not readed in again
	}
	data := readSerializedPage(pageNum)
	if len(data) == 0 {
		return // Nothing to do, empty.
	}
	var newPage PageStore
	if err := json.Unmarshal(data, &newPage); err != nil {
		return // Error deserializing, stop.
	}
	s.pages[pageNum] = &newPage
}

func (s *pagedDNSStore) writePageOut(pageNum uint64) {
	if pageRef, ok := s.pages[pageNum]; ok {
		page := *pageRef // deref!
		data, err := json.Marshal(page)
		if err != nil {
			log.Fatalf(">> Cannot write page out: %+v\n", err)
			return // Failed to serialize
		}
		writeSerializedPage(pageNum, data)
	}
	// Else do nothing, page does not exist
}

// Loads a page into memory given the page number.
// If page does not exist before, create the page.
// Updates LRU list with new page, and potentially dump the LRU element from list to FS and drop from memory
// NOT lock protected
func (s *pagedDNSStore) loadPageAndUpdateLRU(pageNum uint64) {
	// Assumption at this point: page not in LRU list
	s.readPageIn(pageNum)
	// If page is not read in, we assume it is a new page!
	// Initialize this page.
	if _, ok := s.pages[pageNum]; !ok {
		s.pages[pageNum] = &PageStore{
			Key:             pageNum,
			LastCommitIndex: 0, // XXX: can the first commit be at index 0?
			Store:           make(map[string]dnsRRTypeMap),
		}
	}
	// Now s.pages[pageNum] should exist and non-nil
	// Add itself to lruList and keep a reference to the element in itself
	s.pages[pageNum].lruElement = s.lruList.PushBack(s.pages[pageNum])

	// Now update LRU and page certain pages out.
	if s.lruList.Len() > PageStoreMaxLen {
		// Go should be able to handle GC with circular pointers between PageStore and list.Element
		poppedVal := s.lruList.Remove(s.lruList.Front())
		if page, ok := poppedVal.(*PageStore); ok {
			s.writePageOut(page.Key)  // Swap out the page to file.
			delete(s.pages, page.Key) // Drop page from map. Data should be deleted.
		}
		// theoretically, "else" should not be possible...
	}
}

// Get the page that `name` should present on.
// If page not exit, create it on the fly.
// Assumption: this function should always be able to return a non-nil pageStore ptr
func (s *pagedDNSStore) getPage(name string) *PageStore {
	pageKey := getPageNumber(name)
	if _, ok := s.pages[pageKey]; !ok {
		s.loadPageAndUpdateLRU(pageKey)
	} else {
		pageLRUElementPtr := s.pages[pageKey].lruElement
		if pageLRUElementPtr != nil {
			// Mark the current page as most recently used again
			s.lruList.Remove(pageLRUElementPtr)
			s.pages[pageKey].lruElement = s.lruList.PushBack(s.pages[pageKey])
		}
	}
	// Now s.pages[pageKey] should be non-nil
	return s.pages[pageKey]
}

func (s *pagedDNSStore) lookupNameMap(domainName string) (dnsRRTypeMap, bool) {
	typeMap, hasTypeMap := s.getPage(domainName).Store[domainName]
	return typeMap, hasTypeMap
}

func (s *pagedDNSStore) rlockStore() {
	// WARNING: changed to actually use write lock.
	// This is due to paging related mechanism.
	// This is performance-wise not ideal, could be improved.
	s.mu.Lock()
}

func (s *pagedDNSStore) runlockStore() {
	// WARNING: changed to actually use write lock.
	// This is due to paging related mechanism.
	// This is performance-wise not ideal, could be improved.
	s.mu.Unlock()
}

// Forward a new cache record to other machiens in the cluster
func (s *pagedDNSStore) whisperAddCacheRecord(rr dns.RR) {

	for i, peerAddr := range s.cluster {
		// skip this machine
		if i == s.id-1 {
			continue
		}
		log.Printf("Whisper to %v\n", peerAddr)

		// send add cache request to other threads
		go func(peerAddr string) {
			addr := peerAddr + "/addcache"
			req, err := http.NewRequest("PUT", addr, strings.NewReader(rr.String()))
			if err != nil {
				log.Println("Cannot form cache forward request")
			}
			req.ContentLength = int64(len(rr.String()))
			// do not care about response
			http.DefaultClient.Do(req)
		}(peerAddr)
	}
}

func (s *pagedDNSStore) addCacheRecord(rr dns.RR, whisper bool) {
	if rr.Header().Name == "." {
		// ignore dummy entry from 8.8.8.8 DNS
		return
	}

	if whisper {
		// broadcast the cache record to other machines in the cluster
		s.whisperAddCacheRecord(rr)
	}

	// check if domain name is in cache
	domainName := strings.ToLower(rr.Header().Name)
	cacheMap, hasCacheMap := s.cache[domainName]
	if !hasCacheMap {
		cacheMap = make(cacheRRTypeMap)
		s.cache[domainName] = cacheMap
	}

	// check if rr type is in cache
	rType := rr.Header().Rrtype
	cacheRRs, hasCR := cacheMap[rType]
	if !hasCR {
		cacheMap[rType] = make(map[string]cacheRRInfo)
		cacheRRs, _ = cacheMap[rType]
	}

	// use 0 tll for all cache records to generate same key for diff ttl
	ttl := rr.Header().Ttl
	rr.Header().Ttl = 0
	crInfo := cacheRRInfo{ttl, time.Now()}
	cacheRRs[rr.String()] = crInfo
	// restore ttl
	rr.Header().Ttl = ttl
	log.Printf("Added cache %v\n", rr.String())
}

func (s *pagedDNSStore) addCacheRecords(records []dns.RR) {
	for _, rr := range records {
		s.addCacheRecord(rr, true)

	}
}

func (s *pagedDNSStore) getGlueRecords(nsName string) []*dns.RR {
	glueRecords := []*dns.RR{}

	hasPreciseMatch := false
	if typeMap, ok := s.getPage(nsName).Store[nsName]; ok {
		if glueARecords, ok := typeMap[dns.TypeA]; ok {
			for _, glueRecordString := range glueARecords {
				rr, err := dns.NewRR(glueRecordString)
				if err == nil && rr != nil {
					hasPreciseMatch = true
					glueRecords = append(glueRecords, &rr)
				}
			}
		}
	}
	if hasPreciseMatch {
		return glueRecords
	}
	// Try star matching
	var leftLabel, rest string // dummy init s.t. avoids local scope
	rest = nsName
	for {
		leftLabel, rest = popLeftmostLabel(rest)
		if leftLabel == "" {
			break
		}
		hasMatch := false
		// Try with wildcard prefix
		wildcardName := "*." + rest
		if wildcardTypeMap, ok := s.getPage(wildcardName).Store[wildcardName]; ok {
			wildcardGlueRecordList := wildcardTypeMap[dns.TypeA]
			for _, wildCardGlueRecordString := range wildcardGlueRecordList {
				rr, err := dns.NewRR(wildCardGlueRecordString)
				if err == nil && rr != nil {
					hasMatch = true
					glueRecords = append(glueRecords, &rr)
				}
			}
		}
		if hasMatch {
			break
		}
	}
	return glueRecords
}

func (s *pagedDNSStore) getCacheRecords(domainName string, qType uint16) []*dns.RR {
	cacheRecords := []*dns.RR{}
	// check cache
	cacheMap, hasCacheMap := s.cache[domainName]
	if hasCacheMap {
		cacheInfoMap := cacheMap[qType]
		if cacheInfoMap != nil && len(cacheInfoMap) != 0 {
			for crString, crInfo := range cacheInfoMap {
				cr, err := dns.NewRR(crString)
				// check cache validity
				timeDiff := uint32(time.Since(crInfo.createTime).Seconds())
				// newTTL := validCache(&crInfo)
				if err == nil && cr != nil && timeDiff < crInfo.ttl {
					// restore ttl
					cr.Header().Ttl = crInfo.ttl - timeDiff
					cacheRecords = append(cacheRecords, &cr)
				} else {
					// remove invalid cache
					delete(cacheInfoMap, crString)
				}
			}
		}
	}

	return cacheRecords
}

func (s *pagedDNSStore) ProposeAddRR(rrString string) {
	proposal := dnsProposal{
		ProposalType: AddRR,
		RRString:     rrString,
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(proposal); err != nil {
		log.Fatal(err)
	}
	s.proposeC <- buf.String()
}

// XXX: This delete is not really granular, consider improve its API
func (s *pagedDNSStore) ProposeDeleteRRs(name string, rrType uint16) {
	proposal := dnsProposal{
		ProposalType: DeleteRR,
		Name:         name,
		RRType:       rrType,
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(proposal); err != nil {
		log.Fatal(err)
	}
	s.proposeC <- buf.String()
}

// Not protected with lock
func (s *pagedDNSStore) insertRR(rr dns.RR, commitIndex uint64) {
	domainName := strings.ToLower(rr.Header().Name)

	// Check if commit index is higher than applied. If not, skip this insert to keep idempotency
	pagePtr := s.getPage(domainName)
	if pagePtr.LastCommitIndex >= commitIndex {
		return // Skip
	}

	typeMap, hasTypeMap := pagePtr.Store[domainName]
	if !hasTypeMap {
		typeMap = make(dnsRRTypeMap)
		pagePtr.Store[domainName] = typeMap
	}

	_, hasRRs := typeMap[rr.Header().Rrtype]
	if !hasRRs {
		typeMap[rr.Header().Rrtype] = []string{}
	}
	typeMap[rr.Header().Rrtype] = append(typeMap[rr.Header().Rrtype], rr.String())

	// Update commit index for the updated page
	pagePtr.LastCommitIndex = commitIndex
}

// Not protected with lock
func (s *pagedDNSStore) deleteRRs(name string, rrType uint16, commitIndex uint64) {
	// Check if commit index is higher than applied. If not, skip this insert to keep idempotency
	pagePtr := s.getPage(name)
	if pagePtr.LastCommitIndex >= commitIndex {
		return // Skip
	}

	if typeMap, ok := pagePtr.Store[name]; ok {
		if _, ok := typeMap[rrType]; ok {
			delete(pagePtr.Store[name], rrType)
		}
		// put this if in the above then clause?
		if len(typeMap) == 0 {
			delete(pagePtr.Store, name)
		}
	}

	// Update commit index for the updated page, EVEN if noop.
	pagePtr.LastCommitIndex = commitIndex
}

// loadFromZoneFile is removed.
// Supporting loading from zonefile here complicates what are last commit indices

func (s *pagedDNSStore) readCommits(commitC <-chan *commitInfo, errorC <-chan error, isInit bool) {
	// BEGIN BUG
	// KSM: If during initialization, it seems that we should first load the snapshot
	// before replaying any future queries from commitC!
	// The order given in the example boilerplate seems to be buggy here.
	if isInit {
		snapshot, err := s.snapshotter.Load()
		if err != snap.ErrNoSnapshot {
			if err != nil && err != snap.ErrNoSnapshot {
				log.Panic(err)
			}
			log.Printf("loading snapshot at term %d and index %d", snapshot.Metadata.Term, snapshot.Metadata.Index)
			if err := s.recoverFromSnapshot(snapshot.Data); err != nil {
				log.Panic(err)
			}
		}
	}
	// END BUG
	for data := range commitC {
		if data == nil {

			// BEGIN BUG
			// KSM: Delete this isInit clause if it is causing trouble.
			// It seems that snapshots are all loaded at once, and only a single commitC <- nil
			// will be ever fired during init.
			// Therefore, without this change, whenever there exists a single snapshot,
			// we would be stuck forever, since the only moment where we return is when
			// err == snap.ErrNoSnapshot, which could probably never happen!
			// Seems to be a bug from example boilerplate code.
			if isInit {
				return
			}
			// END BUG

			// done replaying log; new data incoming
			// OR signaled to load snapshot
			snapshot, err := s.snapshotter.Load()
			if err == snap.ErrNoSnapshot {
				return
			}
			if err != nil && err != snap.ErrNoSnapshot {
				log.Panic(err)
			}
			log.Printf("loading snapshot at term %d and index %d", snapshot.Metadata.Term, snapshot.Metadata.Index)
			if err := s.recoverFromSnapshot(snapshot.Data); err != nil {
				log.Panic(err)
			}
			continue
		}

		var proposal dnsProposal
		dec := gob.NewDecoder(bytes.NewBufferString(*data.data))
		if err := dec.Decode(&proposal); err != nil {
			log.Fatalf("dnsStore: could not decode message (%v)", err)
		}

		switch proposal.ProposalType {
		case AddRR:
			rr, err := dns.NewRR(proposal.RRString)
			if err == nil && rr != nil {
				s.mu.Lock()
				s.insertRR(rr, data.index)
				s.mu.Unlock()
			}
		case DeleteRR:
			s.mu.Lock()
			s.deleteRRs(proposal.Name, proposal.RRType, data.index)
			s.mu.Unlock()
		default:
			log.Fatalf("dnsStore: bad proposal type (%v)", proposal.ProposalType)
		}
	}
	if err, ok := <-errorC; ok {
		log.Fatal(err)
	}
}

// Snapshots are no longer necessary, since we have our own fs storage for pages.
// These storage should be stable, since we only write what has been committed so far.
// Nothing to snapshot. Keeping these 2 for only commit replaying purposes.
// When restarting, the map would be empty and reconstructed on query.

func (s *pagedDNSStore) getSnapshot() ([]byte, error) {
	return []byte{}, nil
}

func (s *pagedDNSStore) recoverFromSnapshot(snapshot []byte) error {
	return nil
}

/*
Dummy test payloads
curl -d "a.com. IN A 10.0.0.1" -X PUT http://127.0.0.1:9121/add
curl -d "b.com. IN A 10.0.0.2" -X PUT http://127.0.0.1:9121/add
curl -d "c.com. IN A 10.0.0.3" -X PUT http://127.0.0.1:9121/add
curl -d "d.com. IN A 10.0.0.4" -X PUT http://127.0.0.1:9121/add
curl -d "e.com. IN A 10.0.0.5" -X PUT http://127.0.0.1:9121/add
curl -d "f.com. IN A 10.0.0.6" -X PUT http://127.0.0.1:9121/add

curl -d '{"name":"a.com.","rrType":"A"}' -X PUT http://127.0.0.1:9121/delete
curl -d '{"name":"b.com.","rrType":"A"}' -X PUT http://127.0.0.1:9121/delete
curl -d '{"name":"c.com.","rrType":"A"}' -X PUT http://127.0.0.1:9121/delete
curl -d '{"name":"d.com.","rrType":"A"}' -X PUT http://127.0.0.1:9121/delete
curl -d '{"name":"e.com.","rrType":"A"}' -X PUT http://127.0.0.1:9121/delete
curl -d '{"name":"f.com.","rrType":"A"}' -X PUT http://127.0.0.1:9121/delete
*/
