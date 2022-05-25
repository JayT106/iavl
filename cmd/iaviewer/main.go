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
		tree, err = ReadTree(args[1], version, []byte(args[2]))
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
func ReadTree(dir string, version int, prefix []byte) (*iavl.MutableTree, error) {
	db, err := OpenDB(dir)
	if err != nil {
		return nil, err
	}
	if len(prefix) != 0 {
		db = dbm.NewPrefixDB(db, prefix)
	}

	tree, err := iavl.NewMutableTree(db, DefaultCacheSize)
	if err != nil {
		return nil, err
	}
	ver, err := tree.LoadVersion(int64(version))
	fmt.Printf("Got version: %d\n", ver)
	return tree, err
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
	modules := [19]string{
		"capability",
		"params",
		"transfer",
		"staking",
		"slashing",
		"distribution",
		"feegrant",
		"upgrade",
		"authz",
		"evidence",
		"feemarket",
		"gravity",
		"gov",
		"cronos",
		"ibc",
		"bank",
		"mint",
		"acc",
		"evm",
	}

	for idx, mod := range modules {
		prefix := fmt.Sprintf("s/k:%s/", mod)
		tree, err := ReadTree(dbpath, version, []byte(prefix))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s data: %s\n", mod, err)
			continue
		}

		fmt.Printf("iterating over %s  (%d/%d)\n", mod, idx+1, len(modules))
		fmt.Printf("tree size:%d height:%d\n", tree.Size(), tree.Height())
		PrintKeysWithValueSize(tree)
		fmt.Println("")
	}

}

func PrintKeysWithValueSize(tree *iavl.MutableTree) {
	fmt.Println("Printing all keys with hashed values (to detect diff)")
	count := int64(0)
	keySizeTotal := 0
	valueSizeTotal := 0
	keyMaxSize := int64(0)
	valueMaxSize := int64(0)
	tree.Iterate(func(key []byte, value []byte) bool {
		printKey := parseWeaveKey(key)
		digest := sha256.Sum256(value)
		valueSize := len(value)
		fmt.Printf("  %s\n    %X\n", printKey, digest, valueSize)
		count++
		keySizeTotal += len(key)
		valueSizeTotal += len(value)
		keyMaxSize = Max(keyMaxSize, int64(len(key)))
		valueMaxSize = Max(valueMaxSize, int64(len(value)))

		if tree.Size() >= 100 && count%(tree.Size()/100) == 0 {
			fmt.Printf("progress:  %d%%\n", count*100/tree.Size())
		}

		return false
	})
	fmt.Printf("%d keys, keySizeTotal: %d, valueSizeTotal: %d\n", count, keySizeTotal, valueSizeTotal)
	fmt.Printf("avg key size:%d, avg value size:%d\n", int64(keySizeTotal)/count, int64(valueSizeTotal)/count)
	fmt.Printf("max key size:%d, max value size:%d\n", keyMaxSize, valueMaxSize)
}

func Max(x, y int64) int64 {
	if x > y {
		return x
	}
	return y
}
