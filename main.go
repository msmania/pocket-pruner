package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/syndtr/goleveldb/leveldb"
)

var (
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

	var dir string
	if len(os.Args) > 2 {
		dir = os.Args[2]
	} else {
		log.Fatal("Must specify working directory as argument 1")
	}

	var databases []string
	if len(os.Args) > 3 {
		databases = strings.Split(os.Args[3], ",")
	} else {
		databases = []string{"application", "blockstore", "state", "txindexer"}
	}

	log.Println("Pruning before block:", pruneBeforeBlock)

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
				// Value is TxHash.  Keep these as is.
				value = it.Value()
				break
			}
		}

		dstDb.Put(key, value, nil)
	}
	it.Release()
	log.Println("Done - txindexer.db")
}

func pruneBlockstore(
	pruneBeforeBlock int, dir string, wg *sync.WaitGroup, verify bool) {
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

		// block commits
		if strings.HasPrefix(stringKey, "C:") {
			var replaceKey = strings.Replace(stringKey, "C:", "", -1)
			var intKey, err = strconv.Atoi(replaceKey)
			if err != nil {
				log.Fatal("Cannot convert ", replaceKey)
			}
			// C int is 1 lower than height
			if intKey > 1 && (intKey+1) < pruneBeforeBlock {
				dbn.Put(key, nil, nil)
			} else {
				dbn.Put(key, value, nil)
			}
			inserted = true
		}

		// block meta
		if strings.HasPrefix(stringKey, "H:") {
			var replaceKey = strings.Replace(stringKey, "H:", "", -1)
			var intKey, err = strconv.Atoi(replaceKey)
			if err != nil {
				log.Fatal("Cannot convert ", replaceKey)
			}
			if intKey > 1 && intKey < pruneBeforeBlock {
				dbn.Put(key, nil, nil)
			} else {
				dbn.Put(key, value, nil)
			}
			inserted = true
		}

		// block seen commit
		if strings.HasPrefix(stringKey, "SC:") {
			var replaceKey = strings.Replace(stringKey, "SC:", "", -1)
			var intKey, err = strconv.Atoi(replaceKey)
			if err != nil {
				log.Fatal("Cannot convert ", replaceKey)
			}
			if intKey > 1 && intKey < pruneBeforeBlock {
				dbn.Put(key, nil, nil)
			} else {
				dbn.Put(key, value, nil)
			}
			inserted = true
		}

		// block parts
		if strings.HasPrefix(stringKey, "P:") {
			var replaceKey = strings.Replace(stringKey, "P:", "", -1)
			re := regexp.MustCompile(`:(\d)+$`)
			replaceKey = re.ReplaceAllString(replaceKey, "")

			var intKey, err = strconv.Atoi(replaceKey)
			if err != nil {
				log.Fatal("Cannot convert ", replaceKey)
			}
			if intKey > 1 && intKey < pruneBeforeBlock {
				dbn.Put(key, nil, nil)
			} else {
				dbn.Put(key, value, nil)
			}
			inserted = true
		}

		if !inserted {
			dbn.Put(key, value, nil)
		}

	}
	iter.Release()
	log.Println("Done - blockstore.db")
}

func pruneStateDb(
	pruneBeforeBlock int, dir string, wg *sync.WaitGroup, verify bool) {
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
				versionBytes := key[len(prefix):]
				var version, err = strconv.Atoi(string(versionBytes))
				if err != nil {
					log.Fatal("Cannot convert ", string(key))
				}

				if version > 1 && version < pruneBeforeBlock {
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
