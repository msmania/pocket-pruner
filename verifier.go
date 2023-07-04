package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/syndtr/goleveldb/leveldb"
)

func toPrintable(arr []byte) (str string) {
	const printable = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		" 0123456789~!@#$%^&*()_+`{}|[]:\";'<>?,./"
	for _, b := range arr {
		if strings.ContainsRune(printable, rune(b)) {
			str = str + string(b)
		} else {
			str = str + fmt.Sprintf("\\x%02x", b)
		}
	}
	return
}

func recursiveVerify(
	dbNew, dbBase *leveldb.DB, prefix, hash []byte) (bool, int) {
	if len(hash) == 0 {
		return true, 0
	}

	nodeKey := make([]byte, len(prefix), len(prefix)+1+len(hash))
	copy(nodeKey, prefix)
	nodeKey = append(nodeKey, 'n')
	nodeKey = append(nodeKey, hash...)

	baseNode, err := dbBase.Get(nodeKey, nil)
	if err != nil {
		log.Printf("Cannot load %s from the base\n", toPrintable(nodeKey))
		return false, 1
	}

	targetNode, err := dbNew.Get(nodeKey, nil)
	if err != nil {
		log.Printf("Cannot load %s from the targetn", toPrintable(nodeKey))
		return false, 1
	}

	ok := true
	if !bytes.Equal(baseNode, targetNode) {
		log.Printf("Mismatch: %s\n", toPrintable(nodeKey))
		ok = false
	}

	l, r, err := getChildrenFromNode(targetNode)
	if err != nil {
		log.Printf("Broken: %s\n", toPrintable(nodeKey))
		return false, 1
	}

	verified, leftCount := recursiveVerify(dbNew, dbBase, prefix, l)
	if !verified {
		ok = false
	}
	verified, rightCount := recursiveVerify(dbNew, dbBase, prefix, r)
	if !verified {
		ok = false
	}
	return ok, leftCount + rightCount + 1
}

func verifyApplicationDb(dbNew, dbBase *leveldb.DB, pruneBeforeBlock int) {
	ok := true

	var count int
	it := dbBase.NewIterator(nil, nil)
	for it.Next() {
		count++
		key := it.Key()
		value := it.Value()

		maybePruned := false
		for _, prefix := range appDbPrefixes {
			if !bytes.HasPrefix(key, prefix) {
				continue
			}

			keyType := key[len(prefix)]
			switch keyType {
			case 'n':
				// Node records are verified through root records
				maybePruned = true
				count--
				continue
			case 'o':
				verTo := int64(binary.BigEndian.Uint64(key[len(prefix)+1:]))
				if verTo < int64(pruneBeforeBlock) {
					maybePruned = true
				}
			case 'r':
				version := int64(binary.BigEndian.Uint64(key[len(prefix)+1:]))
				if version >= int64(pruneBeforeBlock) {
					v, c := recursiveVerify(dbNew, dbBase, prefix, value)
					count += c
					if !v {
						ok = false
					}
				} else {
					maybePruned = true
				}
			}

			break
		}

		if !maybePruned && len(key) >= 3 && key[2] >= '0' && key[2] <= '9' {
			version, err := strconv.Atoi(string(key[2:]))
			if err == nil && version < pruneBeforeBlock {
				maybePruned = true
			}
		}

		if maybePruned {
			continue
		}

		valueFromTarget, err := dbNew.Get(key, nil)
		if err != nil {
			log.Printf("Cannot load %s\n", toPrintable(key))
			ok = false
			continue
		}

		// Strict check
		if !bytes.Equal(value, valueFromTarget) {
			log.Printf("Mismatch: %s\n", toPrintable(key))
			ok = false
		}
	}
	it.Release()

	if ok {
		log.Printf("Checked %d records in Application.  All good.\n", count)
	} else {
		log.Printf("Checked %d records in Application.  Failed!\n", count)
	}
}

func verifyBlockStore(dbNew, dbBase *leveldb.DB, pruneBeforeBlock int) {
	ok := true

	var count int
	it := dbBase.NewIterator(nil, nil)
	for it.Next() {
		count++
		key := it.Key()
		value := it.Value()

		valueMaybeNull := false
		for _, prefix := range blockstorePrefixes {
			if !bytes.HasPrefix(key, prefix) {
				continue
			}

			var version int
			if prefix[0] == 'P' {
				chunks := bytes.Split(key, []byte{':'})
				if len(chunks) == 3 {
					ver, err1 := strconv.Atoi(string(chunks[1]))
					_, err2 := strconv.Atoi(string(chunks[2]))
					if err1 == nil && err2 == nil {
						version = ver
					}
				}
			} else {
				ver, err := strconv.Atoi(string(key[len(prefix):]))
				if err == nil {
					version = ver
				}
			}

			if version > 1 && version < pruneBeforeBlock {
				valueMaybeNull = true
			}
			break
		}

		valueFromTarget, err := dbNew.Get(key, nil)
		if err != nil {
			log.Printf("Cannot load %s\n", toPrintable(key))
			ok = false
			continue
		}

		if valueMaybeNull {
			if len(valueFromTarget) == 0 {
				// This record has been pruned.
				continue
			}
		}

		// Strict check
		if !bytes.Equal(value, valueFromTarget) {
			log.Printf("Mismatch: %s\n", toPrintable(key))
			ok = false
		}
	}
	it.Release()

	if ok {
		log.Printf("Checked %d records in BlockStore.  All good.\n", count)
	} else {
		log.Printf("Checked %d records in BlockStore.  Failed!\n", count)
	}
}

func verifyStateDb(dbNew, dbBase *leveldb.DB, pruneBeforeBlock int) {
	ok := true

	var count int
	it := dbBase.NewIterator(nil, nil)
	for it.Next() {
		count++
		key := it.Key()
		value := it.Value()

		valueMaybeNull := false
		for _, prefix := range stateDbPrefixes {
			if bytes.HasPrefix(key, prefix) {
				version, err := strconv.Atoi(string(key[len(prefix):]))
				if err == nil && version > 1 && version < pruneBeforeBlock {
					valueMaybeNull = true
				}
				break
			}
		}

		valueFromTarget, err := dbNew.Get(key, nil)
		if err != nil {
			log.Printf("Cannot load %s\n", toPrintable(key))
			ok = false
			continue
		}

		if valueMaybeNull {
			if len(valueFromTarget) == 0 {
				// This record has been pruned.
				continue
			}
		}

		// Strict check
		if !bytes.Equal(value, valueFromTarget) {
			log.Printf("Mismatch: %s\n", toPrintable(key))
			ok = false
		}
	}
	it.Release()

	if ok {
		log.Printf("Checked %d records in State.  All good.\n", count)
	} else {
		log.Printf("Checked %d records in State.  Failed!\n", count)
	}
}

func verifyTxIndexer(dbNew, dbBase *leveldb.DB) {
	ok := true

	var count int
	it := dbBase.NewIterator(nil, nil)
	for it.Next() {
		count++
		key := it.Key()
		value := it.Value()

		valueMaybeNull := true
		for _, prefix := range txIndexerPrefixes {
			if bytes.HasPrefix(key, prefix) {
				valueMaybeNull = false
				break
			}
		}

		valueFromTarget, err := dbNew.Get(key, nil)
		if err != nil {
			log.Printf("Cannot load %s\n", toPrintable(key))
			ok = false
			continue
		}

		if valueMaybeNull {
			if len(valueFromTarget) == 0 {
				// This record has been pruned.
				continue
			}
		}

		// Strict check
		if !bytes.Equal(value, valueFromTarget) {
			log.Printf("Mismatch: %s\n", toPrintable(key))
			ok = false
		}
	}
	it.Release()

	if ok {
		log.Printf("Checked %d records in TxIndexer.  All good.\n", count)
	} else {
		log.Printf("Checked %d records in TxIndexer.  Failed!\n", count)
	}
}
