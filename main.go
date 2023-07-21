package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
)

var (
	// Prefixes for all the different stores: applicationDB, blockstore, stateDB, txIndexer
	//
	// The prefix represents which keys SHOULD be kept. Keys without these prefixes are pruned
	// if before the pruned height provided.

	appDbPrefixes = [][]byte{
		[]byte("s/k:application/"),
		[]byte("s/k:auth/"),
		[]byte("s/k:gov/"),
		[]byte("s/k:main/"),
		[]byte("s/k:params/"),
		[]byte("s/k:pocketcore/"),
		[]byte("s/k:pos/"),
	}
	blockstorePrefixes = [][]byte{
		[]byte("C:"),
		[]byte("H:"),
		[]byte("SC:"),
		[]byte("P:"), // P:%v:%v
	}
	stateDbPrefixes = [][]byte{
		[]byte("abciResponsesKey:"),
		[]byte("consensusParamsKey:"),
		[]byte("validatorsKey:"),
	}
	txIndexerPrefixes = [][]byte{
		[]byte("tx.height/"),
		[]byte("tx.recipient/"),
		[]byte("tx.signer/"),
	}

	// State.DB
	keyGenesisDoc = []byte("genesisDoc")
	keyStateKey   = []byte("stateKey")

	// Appliation.DB
	keyLatest = []byte("s/latest")
)

func main() {
	if len(os.Args) == 1 {
		fmt.Print(`
USAGE:
  pruner <pruneBeforeBlock> <path to data directory> [application,blockstore,state,txindexer]

`)
		return
	}

	// Validate `pruneBeforeBlock` argument
	var pruneBeforeBlock = 0
	if len(os.Args) > 1 {
		var err error
		pruneBeforeBlock, err = strconv.Atoi(os.Args[1])
		if err != nil || pruneBeforeBlock < 1 {
			log.Fatal("Argument 0, pruneBeforeBlock, must be an integer: ", os.Args[1], pruneBeforeBlock)
		}
	} else {
		log.Fatal("Must specify pruneBeforeBlock as argument 0")
	}

	// Validate `dir` argument
	var dir string
	if len(os.Args) > 2 {
		dir = os.Args[2]
	} else {
		log.Fatal("Must specify working directory as argument 1")
	}

	// Prepare `databases` argument
	var databases []string
	if len(os.Args) > 3 {
		databases = strings.Split(os.Args[3], ",")
	} else {
		databases = []string{"application", "blockstore", "state", "txindexer"}
	}

	log.Println("Pruning before block:", pruneBeforeBlock)

	// Prune each database in parallel and wait for all of them to complete before returning
	var wg sync.WaitGroup
	for _, db := range databases {
		verify := false
		if strings.HasSuffix(db, "!") {
			verify = true
			db = db[:len(db)-1]
		}

		switch db {
		case "application":
			wg.Add(1)
			go pruneAppDb(pruneBeforeBlock, dir, &wg, verify)
		case "blockstore":
			wg.Add(1)
			go pruneBlockstore(pruneBeforeBlock, dir, &wg, verify)
		case "state":
			wg.Add(1)
			go pruneStateDb(pruneBeforeBlock, dir, &wg, verify)
		case "txindexer":
			wg.Add(1)
			go pruneTxIndexer(dir, &wg, verify)
		default:
			log.Println("Ignore unknown database:", db, verify)
		}
	}
	wg.Wait()
	log.Println("Completed all tasks.")
}

// pruneTxIndexer prunes the txindexer from dir+"/txindexer.db to dir+"/txindexer-new.db"
// and does not prune any key with the prefix in txIndexerPrefixes.
func pruneTxIndexer(dir string, wg *sync.WaitGroup, verify bool) {
	srcDb, err := leveldb.OpenFile(dir+"/txindexer.db", nil)
	if err != nil {
		log.Fatal("Failed to open txindexer" + dir)
	}
	dstDb, err := leveldb.OpenFile(dir+"/txindexer-new.db", nil)
	if err != nil {
		log.Fatal("Failed to open new txindexer copy")
	}

	defer func() {
		srcDb.Close()
		dstDb.Close()
		wg.Done()
	}()

	if verify {
		verifyTxIndexer(dstDb, srcDb)
		return
	}

	it := srcDb.NewIterator(nil, nil)
	for it.Next() {
		key := it.Key()

		// Drop value of TxResult if the key is not in txIndexerPrefixes
		var value []byte

		for _, prefix := range txIndexerPrefixes {
			if bytes.HasPrefix(key, prefix) {
				// Value is TxHash. DO NOT prune.
				value = it.Value()
				break
			}
		}

		dstDb.Put(key, value, nil)
	}
	it.Release()
	log.Println("Done - txindexer.db")
}

// pruneBlockstore prunes the blockstore from dir+"/blockstore.db" to dir+"/blockstore-new.db"
// and does not prune any key with the prefix in blockstorePrefixes.
func pruneBlockstore(
	pruneBeforeBlock int,
	dir string,
	wg *sync.WaitGroup,
	verify bool,
) {
	dbb, err := leveldb.OpenFile(dir+"/blockstore.db", nil)
	if err != nil {
		log.Fatal("Failed to open blockstore" + dir)
	}
	dbn, err := leveldb.OpenFile(dir+"/blockstore-new.db", nil)
	if err != nil {
		log.Fatal("Failed to open new blockstore copy")
	}

	defer func() {
		dbb.Close()
		dbn.Close()
		wg.Done()
	}()

	if verify {
		verifyBlockStore(dbn, dbb, pruneBeforeBlock)
		return
	}

	iter := dbb.NewIterator(nil, nil)
	for iter.Next() {
		key := iter.Key()
		value := iter.Value()
		var stringKey = string(key)
		var inserted = false

		for _, prefix := range blockstorePrefixes {
			if bytes.HasPrefix([]byte(stringKey), prefix) {
				chunks := strings.SplitN(stringKey, ":", 3)
				if len(chunks) < 2 {
					log.Fatal("Cannot convert ", stringKey)
				}
				var intKey, err = strconv.Atoi(chunks[1])
				if err != nil {
					log.Fatal("Cannot convert ", stringKey)
				}

				pruneBeforeBlockToCheck := pruneBeforeBlock
				if strings.HasPrefix(stringKey, "C:") {
					// Why decrement here?
					// height-1 is set to the integer part of the blockCommit key.
					// See store.SaveBlock in tendermint.
					pruneBeforeBlockToCheck--
				}

				if intKey > 1 && intKey < pruneBeforeBlockToCheck {
					dbn.Put(key, nil, nil)
				} else {
					dbn.Put(key, value, nil)
				}
				inserted = true
			}
		}

		if !inserted {
			dbn.Put(key, value, nil)
		}
	}
	iter.Release()
	log.Println("Done - blockstore.db")
}

// pruneStateDb prunes the state.db from dir+"/state.db" to dir+"/state-new.db"
// and does not prune any key with the prefix in stateDbPrefixes.
func pruneStateDb(
	pruneBeforeBlock int,
	dir string,
	wg *sync.WaitGroup,
	verify bool,
) {
	srcDb, err := leveldb.OpenFile(dir+"/state.db", nil)
	if err != nil {
		log.Fatal("Failed to open state" + dir)
	}
	dstDb, err := leveldb.OpenFile(dir+"/state-new.db", nil)
	if err != nil {
		log.Fatal("Failed to open new state copy")
	}

	defer func() {
		srcDb.Close()
		dstDb.Close()
		wg.Done()
	}()

	if verify {
		verifyStateDb(dstDb, srcDb, pruneBeforeBlock)
		return
	}

	iter := srcDb.NewIterator(nil, nil)
	for iter.Next() {
		key := iter.Key()
		value := iter.Value()
		inserted := false

		for _, prefix := range stateDbPrefixes {
			if bytes.HasPrefix(key, prefix) {
				heightBytes := key[len(prefix):]
				var height, err = strconv.Atoi(string(heightBytes))
				if err != nil {
					log.Fatal("Cannot convert ", string(key))
				}

				if height > 1 && height < pruneBeforeBlock {
					dstDb.Put(key, nil, nil)
				} else {
					dstDb.Put(key, value, nil)
				}
				inserted = true
				break
			}
		}

		if !inserted {
			if !bytes.HasPrefix(key, keyGenesisDoc) &&
				!bytes.HasPrefix(key, keyStateKey) {
				log.Println("Unknown key: ", string(key))
			}
			dstDb.Put(key, value, nil)
		}
	}
	iter.Release()
	log.Println("Done - state.db")
}
