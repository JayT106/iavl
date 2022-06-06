package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/address"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/iavl"
	dbm "github.com/tendermint/tm-db"
	ethermint "github.com/tharsis/ethermint/types"
)

// TODO: make this configurable?
const (
	DefaultCacheSize int = 10000
)

func main() {
	args := os.Args[1:]
	if len(args) < 3 ||
		(args[0] != "data" &&
			args[0] != "shape" &&
			args[0] != "versions" &&
			args[0] != "balance" &&
			args[0] != "nonce" &&
			args[0] != "stastistics") {
		fmt.Fprintln(os.Stderr, "Usage: iaviewer <data|shape|versions> <leveldb dir> <prefix> [version number]")
		fmt.Fprintln(os.Stderr, "<prefix> is the prefix of db, and the iavl tree of different modules in cosmos-sdk uses ")
		fmt.Fprintln(os.Stderr, "different <prefix> to identify, just like \"s/k:gov/\" represents the prefix of gov module")
		os.Exit(1)
	}

	version := 0
	if len(args) >= 4 {
		var err error
		version, err = strconv.Atoi(args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid version number: %s\n", err)
			os.Exit(1)
		}
	}

	var tree *iavl.MutableTree
	if args[0] != "stastistics" {
		var err error
		tree, _, err = ReadTree(args[1], version, []byte(args[2]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading data: %s\n", err)
			os.Exit(1)
		}
	}

	switch args[0] {
	case "data":
		PrintKeys(tree)
		fmt.Printf("Hash: %X\n", tree.Hash())
		fmt.Printf("Size: %X\n", tree.Size())
	case "shape":
		PrintShape(tree)
	case "versions":
		PrintVersions(tree)
	case "balance":
		addr, err := hex.DecodeString(args[4])
		if err != nil {
			panic(err)
		}
		PrintBalance(tree, addr)
	case "nonce":
		addr, err := hex.DecodeString(args[4])
		if err != nil {
			panic(err)
		}
		PrintAccount(tree, addr)
	case "stastistics":
		PrintStatistics(args[1], version)
	}
}

func OpenDB(dir string) (dbm.DB, error) {
	switch {
	case strings.HasSuffix(dir, ".db"):
		dir = dir[:len(dir)-3]
	case strings.HasSuffix(dir, ".db/"):
		dir = dir[:len(dir)-4]
	default:
		return nil, fmt.Errorf("database directory must end with .db")
	}
	// TODO: doesn't work on windows!
	cut := strings.LastIndex(dir, "/")
	if cut == -1 {
		return nil, fmt.Errorf("cannot cut paths on %s", dir)
	}
	name := dir[cut+1:]
	db, err := dbm.NewRocksDB(name, dir[:cut])
	if err != nil {
		return nil, err
	}
	return db, nil
}

// nolint: unused,deadcode
func PrintDBStats(db dbm.DB) {
	count := 0
	prefix := map[string]int{}
	itr, err := db.Iterator(nil, nil)
	if err != nil {
		panic(err)
	}

	defer itr.Close()
	for ; itr.Valid(); itr.Next() {
		key := string(itr.Key()[:1])
		prefix[key]++
		count++
	}
	if err := itr.Error(); err != nil {
		panic(err)
	}
	fmt.Printf("DB contains %d entries\n", count)
	for k, v := range prefix {
		fmt.Printf("  %s: %d\n", k, v)
	}
}

// ReadTree loads an iavl tree from the directory
// If version is 0, load latest, otherwise, load named version
// The prefix represents which iavl tree you want to read. The iaviwer will always set a prefix.
func ReadTree(dir string, version int, prefix []byte) (*iavl.MutableTree, dbm.DB, error) {
	db, err := OpenDB(dir)
	if err != nil {
		return nil, nil, err
	}
	if len(prefix) != 0 {
		db = dbm.NewPrefixDB(db, prefix)
	}

	tree, err := iavl.NewMutableTree(db, DefaultCacheSize)
	if err != nil {
		db.Close()
		return nil, nil, err
	}

	fmt.Printf("iterating over tree...\n")
	tree.AllKVSize()
	fmt.Printf("iterating over tree...done\n")

	ver, err := tree.LoadVersion(int64(version))
	// if err != nil {
	// 	db.Close()
	// 	return nil, nil, err
	// }

	//s, k, v := tree.GetOrphanSize()

	fmt.Printf("Got version: %d\n", ver)
	//fmt.Printf("orphans: %d,totalKSize: %d, totalVSize: %d\n", s, k, v)

	return tree, db, err
}

func PrintKeys(tree *iavl.MutableTree) {
	fmt.Println("Printing all keys with hashed values (to detect diff)")
	tree.Iterate(func(key []byte, value []byte) bool {
		printKey := parseWeaveKey(key)
		digest := sha256.Sum256(value)
		fmt.Printf("  %s\n    %X\n", printKey, digest)
		return false
	})
}

// parseWeaveKey assumes a separating : where all in front should be ascii,
// and all afterwards may be ascii or binary
func parseWeaveKey(key []byte) string {
	cut := bytes.IndexRune(key, ':')
	if cut == -1 {
		return encodeID(key)
	}
	prefix := key[:cut]
	id := key[cut+1:]
	return fmt.Sprintf("%s:%s", encodeID(prefix), encodeID(id))
}

// casts to a string if it is printable ascii, hex-encodes otherwise
func encodeID(id []byte) string {
	for _, b := range id {
		if b < 0x20 || b >= 0x80 {
			return strings.ToUpper(hex.EncodeToString(id))
		}
	}
	return string(id)
}

func PrintShape(tree *iavl.MutableTree) {
	// shape := tree.RenderShape("  ", nil)
	shape := tree.RenderShape("  ", nodeEncoder)
	fmt.Println(strings.Join(shape, "\n"))
}

func nodeEncoder(id []byte, depth int, isLeaf bool) string {
	prefix := fmt.Sprintf("-%d ", depth)
	if isLeaf {
		prefix = fmt.Sprintf("*%d ", depth)
	}
	if len(id) == 0 {
		return fmt.Sprintf("%s<nil>", prefix)
	}
	return fmt.Sprintf("%s%s", prefix, parseWeaveKey(id))
}

func PrintVersions(tree *iavl.MutableTree) {
	versions := tree.AvailableVersions()
	fmt.Println("Available versions:")
	for _, v := range versions {
		fmt.Printf("  %d\n", v)
	}
}

func PrintBalance(tree *iavl.MutableTree, addr []byte) {
	key := []byte{0x02}
	key = append(key, address.MustLengthPrefix(addr)...)
	denom := "basecro"
	key = append(key, []byte(denom)...)
	_, value := tree.Get(key)
	if value == nil {
		fmt.Println("not found")
	} else {
		cdc := codec.NewLegacyAmino()
		marshaler := codec.NewAminoCodec(cdc)
		var balance sdk.Coin
		marshaler.MustUnmarshal(value, &balance)
		fmt.Println(balance.String())
	}
}

func PrintAccount(tree *iavl.MutableTree, addr []byte) {
	key := authtypes.AddressStoreKey(addr)
	_, value := tree.Get(key)
	if value == nil {
		fmt.Println("not found")
	} else {
		interfaceRegistry := types.NewInterfaceRegistry()
		authtypes.RegisterInterfaces(interfaceRegistry)
		ethermint.RegisterInterfaces(interfaceRegistry)
		marshaler := codec.NewProtoCodec(interfaceRegistry)

		var acc authtypes.AccountI
		if err := marshaler.UnmarshalInterface(value, &acc); err != nil {
			panic(err)
		}
		fmt.Println(acc.GetSequence())
	}
}

func PrintStatistics(dbpath string, version int) {
	// prefixes "s/k:bank/"
	modules := [1]string{
		// "capability",
		// "params",
		// "transfer",
		// "staking",
		// "slashing",
		// "distribution",
		// "feegrant",
		// "upgrade",
		// "authz",
		// "evidence",
		// "feemarket",
		// "gravity",
		// "gov",
		// "cronos",
		// "ibc",
		// "bank",
		// "mint",
		// "acc",
		"evm",
	}

	for _, mod := range modules {
		prefix := fmt.Sprintf("s/k:%s/", mod)
		_, db, err := ReadTree(dbpath, version, []byte(prefix))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s data: %s\n", mod, err)
			continue
		}

		//fmt.Printf("tree size:%d height:%d\n", tree.Size(), tree.Height())
		//PrintKeysWithValueSize(tree)

		db.Close()
	}

}

func PrintKeysWithValueSize(tree *iavl.MutableTree) {
	fmt.Println("Printing all keys with hashed values (to detect diff)")
	leafCount := int64(0)
	nonLeafCount := int64(0)
	keySizeLeafTotal := 0
	valueSizeLeafTotal := 0
	//keyMaxLeafSize := int64(0)
	//valueMaxLeafSize := int64(0)
	encodedSizeLeafTotal := int64(0)

	keySizeNonLeafTotal := 0
	valueSizeNonLeafTotal := 0
	//keyMaxNonLeafSize := int64(0)
	//valueMaxNonLeafSize := int64(0)
	encodedSizeNonLeafTotal := int64(0)

	tree.IterateNode(func(key []byte, value []byte, leaf bool, encodedSize int) bool {
		//printKey := parseWeaveKey(key)
		//digest := sha256.Sum256(value)
		//valueSize := len(value)
		//fmt.Printf("k: %s,leaf: %t,vs: %d,es: %d\n", printKey, leaf, valueSize, encodedSize)

		if leaf {
			leafCount++
			keySizeLeafTotal += len(key)
			valueSizeLeafTotal += len(value)
			encodedSizeLeafTotal += int64(encodedSize)
			//keyMaxLeafSize = Max(keyMaxLeafSize, int64(len(key)))
			//valueMaxLeafSize = Max(valueMaxLeafSize, int64(len(value)))
		} else {
			nonLeafCount++
			keySizeNonLeafTotal += len(key)
			valueSizeNonLeafTotal += len(value)
			encodedSizeNonLeafTotal += int64(encodedSize)
			//keyMaxNonLeafSize = Max(keyMaxNonLeafSize, int64(len(key)))
			//valueMaxNonLeafSize = Max(valueMaxNonLeafSize, int64(len(value)))
		}

		return false
	})
	fmt.Printf("leaf- k: %d, kst: %d, vst: %d, est: %d\n", leafCount, keySizeLeafTotal, valueSizeLeafTotal, encodedSizeLeafTotal)
	fmt.Printf("nonLeaf- k: %d, kst: %d, vst: %d, est: %d\n", nonLeafCount, keySizeNonLeafTotal, valueSizeNonLeafTotal, encodedSizeNonLeafTotal)
}

func Max(x, y int64) int64 {
	if x > y {
		return x
	}
	return y
}
