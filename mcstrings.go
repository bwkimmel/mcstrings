package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/binary"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

var (
	world  = flag.String("world", "", "Path to the world to scan (the directory containing level.dat)")
	filter = flag.String("filter", "all", fmt.Sprintf("Only include entries matching a filter (one of: %s)", validOutputFilters()))
	invert = flag.Bool("invert", false, "Output entries *not* matching the filter")

	compressionFilters = map[int8]func(io.Reader) (io.ReadCloser, error){
		1: newGZipFilter,
		2: zlib.NewReader,
		3: newIdentFilter,
	}

	outputFilters = map[string]func(_, _ string) bool{
		"all":       func(_, _ string) bool { return true },
		"user_text": containsUserText,
	}

	pagesRE = regexp.MustCompile(`.*/pages\[\d+\]$`)
	signRE  = regexp.MustCompile(`.*/text\d+$`)
)

type output struct {
	csv    *csv.Writer
	filter func(_, _ string) bool
}

func validOutputFilters() string {
	var names []string
	for k, _ := range outputFilters {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}

func clean(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func containsUserText(k, v string) bool {
	v = clean(v)
	if v == "" {
		return false
	}
	if v == "null" {
		return false
	}
	if v == `{"text":""}` {
		return false
	}

	k = clean(k)
	if strings.HasSuffix(k, "/display/name") {
		return true
	}
	if strings.HasSuffix(k, "/customname") {
		return true
	}
	if strings.HasSuffix(k, "/title") {
		return true
	}
	if pagesRE.MatchString(k) {
		return true
	}
	if signRE.MatchString(k) {
		return true
	}
	return false
}

func newGZipFilter(r io.Reader) (io.ReadCloser, error) {
	return gzip.NewReader(r)
}

func newIdentFilter(r io.Reader) (io.ReadCloser, error) {
	return ioutil.NopCloser(r), nil
}

func main() {
	flag.Parse()
	if *world == "" {
		log.Fatal("--world is required")
	}
	of, ok := outputFilters[*filter]
	if !ok {
		log.Fatalf("Invalid value for --filter (%s), must be one of %s.", *filter, validOutputFilters())
	}
	if *invert {
		orig := of
		of = func(k, v string) bool {
			return !orig(k, v)
		}
	}
	out := &output{
		csv:    csv.NewWriter(os.Stdout),
		filter: of,
	}
	if err := readWorld(*world, out); err != nil {
		log.Fatalf("ERROR: %v", err)
	}
}

func join(a, b string) string {
	if len(b) == 0 {
		return a
	}
	if b[0] == '[' {
		return a + b
	}
	return a + "/" + b
}

func findStrings(x interface{}, cb func(path, value string)) {
	switch value := x.(type) {
	case string:
		cb("", value)
	case map[string]interface{}:
		var keys []string
		for k, _ := range value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := value[k]
			findStrings(v, func(path, value string) {
				cb(join(k, path), value)
			})
		}
	case []interface{}:
		for i, v := range value {
			findStrings(v, func(path, value string) {
				cb(join(fmt.Sprintf("[%d]", i), path), value)
			})
		}
	}
}

func readWorld(path string, out *output) error {
	if err := readDimension(0, filepath.Join(path, "region"), out); err != nil {
		return err
	}
	if err := readDimension(-1, filepath.Join(path, "DIM-1"), out); err != nil {
		return err
	}
	if err := readDimension(1, filepath.Join(path, "DIM1"), out); err != nil {
		return err
	}
	return nil
}

func readDimension(dim int, path string, out *output) error {
	dir, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read contents of directory %q: %v", path, err)
	}

	for _, e := range dir {
		if !strings.HasSuffix(e.Name(), ".mca") {
			continue
		}
		var x, z int
		region := filepath.Join(path, e.Name())
		if _, err := fmt.Sscanf(e.Name(), "r.%d.%d.mca", &x, &z); err != nil {
			return fmt.Errorf("invalid region file name %q", region)
		}
		readRegion(dim, x, z, region, out)
	}
	return nil
}

func readRegion(dim, x, z int, path string, out *output) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open region file %q: %v", path, err)
	}
	defer f.Close()

	locs := make([]uint32, 1024)
	if err := binary.Read(f, binary.BigEndian, &locs); err != nil {
		return fmt.Errorf("cannot read location data from region file %q: %v", path, err)
	}

	for i, loc := range locs {
		if loc == 0 {
			continue
		}
		dx, dz := i%32, i/32
		offset := int64(4096 * (loc & 0xffffff00) >> 8)
		size := int64(4096 * (loc & 0xff))
		if _, err := f.Seek(offset, 0); err != nil {
			return fmt.Errorf("cannot seek to chunk %d in region file %q: %v", i, path, err)
		}
		chunk, err := readChunk(&io.LimitedReader{f, size})
		if err != nil {
			return fmt.Errorf("cannot read chunk %d in region file %q: %v", i, path, err)
		}
		findStrings(chunk, func(path, value string) {
			if !out.filter(path, value) {
				return
			}
			out.csv.Write([]string{
				strconv.Itoa(dim),
				strconv.Itoa(x*32 + dx),
				strconv.Itoa(z*32 + dz),
				path,
				value,
			})
		})
		out.csv.Flush()
		if err := out.csv.Error(); err != nil {
			return fmt.Errorf("cannot write output: %v", err)
		}
	}
	return nil
}

func readChunk(r io.Reader) (map[string]interface{}, error) {
	var (
		length      int32
		compression int8
	)
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("cannot read chunk length: %v", err)
	}
	if err := binary.Read(r, binary.BigEndian, &compression); err != nil {
		return nil, fmt.Errorf("cannot read compression tag: %v", err)
	}
	cf, ok := compressionFilters[compression]
	if !ok {
		return nil, fmt.Errorf("invalid compression tag: %d", compression)
	}
	data := make([]byte, length-1)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("cannot read chunk data: %v", err)
	}
	nbtr, err := cf(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("cannot decompress chunk data: %v", err)
	}
	defer nbtr.Close()
	nbtData, err := ioutil.ReadAll(nbtr)
	if err != nil {
		return nil, fmt.Errorf("cannot read NBT data: %v", err)
	}
	var m map[string]interface{}
	if err := nbt.UnmarshalEncoding(nbtData, &m, nbt.BigEndian); err != nil {
		return nil, fmt.Errorf("cannot decode NBT data: %v", err)
	}
	return m, nil
}
