package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/util"
	"github.com/tendermint/go-amino"
)

func getChildrenFromNode(buf []byte) (leftHash, rightHash []byte, err error) {
	height, n, cause := amino.DecodeInt8(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.height")
		return
	}

	if height == 0 {
		// this node is a leaf
		return
	}

	buf = buf[n:]

	_, n, cause = amino.DecodeVarint(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.size")
		return
	}
	buf = buf[n:]

	_, n, cause = amino.DecodeVarint(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.version")
		return
	}
	buf = buf[n:]

	_, n, cause = amino.DecodeByteSlice(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.key")
		return
	}
	buf = buf[n:]

	leftHash, n, cause = amino.DecodeByteSlice(buf)
	if cause != nil {
		err = errors.Wrap(cause, "deocding node.leftHash")
		return
	}
	buf = buf[n:]

	rightHash, _, cause = amino.DecodeByteSlice(buf)
	if cause != nil {
		err = errors.Wrap(cause, "decoding node.rightHash")
		return
	}

	return
}

func recursiveTreeCopy(srcDb, dstDb *leveldb.DB, prefix, hash []byte) {
	if len(hash) == 0 {
		return
	}

	nodeKey := make([]byte, len(prefix), len(prefix)+1+len(hash))
	copy(nodeKey, prefix)
	nodeKey = append(nodeKey, 'n')
	nodeKey = append(nodeKey, hash...)

	nodeValue, err := srcDb.Get(nodeKey, nil)
	if err != nil {
		log.Fatalf("Not found: %x", nodeKey)
		return
	}
	dstDb.Put(nodeKey, nodeValue, nil)

	l, r, err := getChildrenFromNode(nodeValue)
	if err != nil {
		log.Fatalf("%s: %s", err.Error(), nodeKey)
		return
	}

	recursiveTreeCopy(srcDb, dstDb, prefix, l)
	recursiveTreeCopy(srcDb, dstDb, prefix, r)
}

func unmarshalBinaryLengthPrefixed(
	codec *amino.Codec, bz []byte, ptr interface{}) error {
	if len(bz) == 0 {
		return errors.New("Cannot decode empty bytes")
	}

	// Read byte-length prefix.
	u64, n := binary.Uvarint(bz)
	if n < 0 {
		return errors.New("Error reading msg byte-length prefix")
	}
	if u64 > uint64(len(bz)-n) {
		return errors.New("Not enough bytes to read")
	} else if u64 < uint64(len(bz)-n) {
		return errors.New("Bytes left over")
	}
	bz = bz[n:]
	return codec.UnmarshalBinaryBare(bz, ptr)
}

func pruneAppDb(
	pruneBeforeBlock int, dir string, wg *sync.WaitGroup, verify bool) {
	srcDb, err := leveldb.OpenFile(dir+"/application.db", nil)
	if err != nil {
		log.Fatal("Failed to open txindexer" + dir)
	}
	dstDb, err := leveldb.OpenFile(dir+"/application-new.db", nil)
	if err != nil {
		log.Fatal("Failed to open new application copy")
	}

	defer func() {
		srcDb.Close()
		dstDb.Close()
		wg.Done()
	}()

	if verify {
		verifyApplicationDb(dstDb, srcDb, pruneBeforeBlock)
		return
	}

	// 1. LatestVersion: s/latest
	latestBytes, err := srcDb.Get(keyLatest, nil)
	if err != nil {
		log.Fatal("Not found: ", string(keyLatest))
	}

	dstDb.Put(keyLatest, latestBytes, nil)

	// 2. CommitInfo: s/<height>
	codec := amino.NewCodec()
	var latest int64
	err = unmarshalBinaryLengthPrefixed(codec, latestBytes, &latest)
	if err != nil {
		log.Fatal(err.Error())
	}

	for i := int64(pruneBeforeBlock); i <= latest; i++ {
		commitInfoKey := []byte(fmt.Sprintf("s/%d", i))
		if cInfoBytes, err := srcDb.Get(commitInfoKey, nil); err == nil {
			dstDb.Put(commitInfoKey, cInfoBytes, nil)
		}
	}

	for _, prefix := range appDbPrefixes {
		log.Println("application.db -", string(prefix))
		var count int64

		iter := srcDb.NewIterator(util.BytesPrefix(prefix), nil)
		for iter.Next() {
			key := iter.Key()
			value := iter.Value()
			inserted := false

			if len(key) == len(prefix) {
				log.Fatal("Unknown: ", string(key))
			}
			keyType := key[len(prefix)]

			switch keyType {
			case 'n':
				// Node records are written through root records
				inserted = true
			case 'o':
				verTo := int64(binary.BigEndian.Uint64(key[len(prefix)+1:]))
				if verTo >= int64(pruneBeforeBlock) {
					dstDb.Put(key, value, nil)
				}
				inserted = true
			case 'r':
				version := int64(binary.BigEndian.Uint64(key[len(prefix)+1:]))
				if version >= int64(pruneBeforeBlock) {
					dstDb.Put(key, value, nil)
					recursiveTreeCopy(srcDb, dstDb, prefix, value)
				}
				inserted = true
			default:
				log.Fatal("Unknown: ", string(key))
			}

			if !inserted {
				log.Fatal("Unknown: ", string(key))
			}

			count++
			if count%100000000 == 0 {
				log.Println("application.db -", string(prefix), count)
			}
		}
		iter.Release()
	}
	log.Println("Done - application.db")
}
