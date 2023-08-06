package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"sync"

	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/tendermint/go-amino"
)

// getChildrenFromNode returns the left and right children of a node.
// This function is based on iavl.MakeNode in pocket-core.
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
		err = errors.Wrap(cause, "decoding node.leftHash")
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

// recursiveTreeCopy does a deep copy of the tree from srcDb to dstDb.
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

// unmarshalBinaryLengthPrefixed unmarshals a binary length-prefixed object.
func unmarshalBinaryLengthPrefixed(codec *amino.Codec, bz []byte, ptr interface{}) error {
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

// pruneAppDb prunes the application database from dir+"/application.db to dir+"/application-new.db"
// and does not prune any key with the prefix in appDbPrefixes.
func pruneAppDb(
	pruneBeforeBlock int,
	dir string,
	wg *sync.WaitGroup,
	verify bool,
) {
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

	// 3. IAVL+ tree: s/k:<namespace>/r<height>
	for i := int64(pruneBeforeBlock); i <= latest; i++ {
		log.Println("application.db - block", i)
		for _, prefix := range appDbPrefixes {
			key := append(prefix, 'r')
			key = binary.BigEndian.AppendUint64(key, uint64(i))
			value, err := srcDb.Get(key, nil)
			if err != nil {
				log.Fatal(err.Error())
			}
			dstDb.Put(key, value, nil)
			recursiveTreeCopy(srcDb, dstDb, prefix, value)
		}
	}
	log.Println("Done - application.db")
}
