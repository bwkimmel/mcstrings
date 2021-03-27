package commands

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
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

	"github.com/google/subcommands"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

var (
	// outputFilters defines the predicates used for filtering NBT data from the
	// emitted results.
	outputFilters = map[string]func(k, v string) bool{
		"all":       func(_, _ string) bool { return true },
		"user_text": containsUserText,
	}

	pagesRE = regexp.MustCompile(`.*/pages\[\d+\]$`)
	signRE  = regexp.MustCompile(`.*/text\d+$`)
)

// Extract implements the extract command.
type Extract struct {
	world  string
	filter string
	invert bool
	header bool
	output string
	csv    *csv.Writer
	keep   func(k, v string) bool
}

// validOutputFilters returns a comma-separated list of valid output filter
// names for usage documentation.
func validOutputFilters() string {
	var names []string
	for k, _ := range outputFilters {
		names = append(names, k)
	}
	return strings.Join(names, ", ")
}

// clean canonicalizes a string for comparisons by trimming whitespace and
// converting it to lowercase.
func clean(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// containsUserText determines if a NBT entry likely contains user-generated
// text. This includes sign text, book contents & titles, renamed items, etc.,
// but excludes entries with empty values (empty strings, null JSON objects,
// signs with empty text).
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

// wrapReader wraps a reader to apply the specified decompression algorithm. See
// https://minecraft.gamepedia.com/Region_file_format#Chunk_data for valid
// compression algorithms.
func wrapReader(r io.Reader, compression int8) (io.ReadCloser, error) {
	switch compression {
	case 1:
		return gzip.NewReader(r)
	case 2:
		return zlib.NewReader(r)
	case 3:
		return ioutil.NopCloser(r), nil
	default:
		return nil, fmt.Errorf("invalid compression type: %d", compression)
	}
}

// join combines two segments of an NBT path.
func join(a, b string) string {
	if len(b) == 0 {
		return a
	}
	if b[0] == '[' {
		return a + b
	}
	return a + "/" + b
}

// findStrings enumerates the strings within an NBT object, calling the
// provided callback function with the path and value of each string.
//
// If x is a string (TAG_String), cb is invoked with the value of the string
// itself.
// If x is a []interface{} (TAG_List), findStrings searches for strings within
// each element.
// If x is a map[string]interface{} (TAG_Compound), findStrings searches for
// strings within each value in the map.
// If x is a numeric type or an array of numeric types, then there are no
// strings.
//
// See https://minecraft.gamepedia.com/NBT_format
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

// readWorld processes the Minecraft world contained in the specified path. The
// path should point to the directory containing the world's level.dat file.
// See https://minecraft.gamepedia.com/Java_Edition_level_format.
func (e *Extract) readWorld(path string) error {
	if err := e.readDimension(0, filepath.Join(path, "region")); err != nil {
		return err
	}
	if err := e.readDimension(-1, filepath.Join(path, "DIM-1", "region")); err != nil {
		return err
	}
	if err := e.readDimension(1, filepath.Join(path, "DIM1", "region")); err != nil {
		return err
	}
	return nil
}

// readDimension processes the Minecraft dimension contained in the specified
// path. The path should point to the directory containing the .mca files for
// the dimension. Dim indicates which dimension is being processed, and should
// be 0 for overworld, -1 for nether, and 1 for the end.
func (e *Extract) readDimension(dim int, path string) error {
	dir, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("cannot read contents of directory %q: %v", path, err)
	}

	for _, entry := range dir {
		if !strings.HasSuffix(entry.Name(), ".mca") {
			continue
		}
		var x, z int
		region := filepath.Join(path, entry.Name())
		if _, err := fmt.Sscanf(entry.Name(), "r.%d.%d.mca", &x, &z); err != nil {
			return fmt.Errorf("invalid region file name %q", region)
		}
		e.readRegion(dim, x, z, region)
	}
	return nil
}

// readRegion processes a single region contained in the specified file. The
// path should point to an .mca file. Dim indicates the dimension containing
// this region (see readDimension). X and Z are the coordinates of the region
// (which are part of the file name).
// See https://minecraft.gamepedia.com/Region_file_format.
func (e *Extract) readRegion(dim, x, z int, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open region file %q: %v", path, err)
	}
	defer f.Close()

	// The first 4kB contains 1024 location entries, which indicate where in this
	// file to find the data for each of the 1024 chunks (32 x 32) in this region.
	// Each location entry contains a 3-byte file offset (in units of 4k sectors)
	// and a one byte sector count.
	// See https://minecraft.gamepedia.com/Region_file_format#Chunk_location.
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
			if !e.keep(path, value) {
				return
			}
			e.csv.Write([]string{
				strconv.Itoa(dim),
				strconv.Itoa(x*32 + dx),
				strconv.Itoa(z*32 + dz),
				path,
				value,
			})
		})
		e.csv.Flush()
		if err := e.csv.Error(); err != nil {
			return fmt.Errorf("cannot write output: %v", err)
		}
	}
	return nil
}

// readChunk reads chunk data and returns a map containing the chunk's NBT tree.
// See https://minecraft.gamepedia.com/Region_file_format#Chunk_data,
// https://minecraft.gamepedia.com/Chunk_format.
func readChunk(r io.Reader) (map[string]interface{}, error) {
	var (
		length      int32
		compression int8
	)
	// The first four bytes of the chunk contain the (compressed) length,
	// excluding these four bytes, but including the compression type below.
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		if err == io.EOF {
			return nil, err
		}
		return nil, fmt.Errorf("cannot read chunk length: %v", err)
	}
	// The next byte contains the compression type.
	if err := binary.Read(r, binary.BigEndian, &compression); err != nil {
		return nil, fmt.Errorf("cannot read compression type: %v", err)
	}
	// The remaining length-1 bytes contains the (possibly-compressed) chunk data
	// in NBT format.
	data := make([]byte, length-1)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("cannot read chunk data: %v", err)
	}
	nbtr, err := wrapReader(bytes.NewReader(data), compression)
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

func (*Extract) Name() string {
	return "extract"
}

func (*Extract) Synopsis() string {
	return "Extract strings from a Minecraft world."
}

func (*Extract) Usage() string {
	return `extract [<flags>...] <world>
Extract strings from a Minecraft world.

Extract strings from the Minecraft world located in the directory <world>.
This should be the directory containing level.dat. The strings will be output
in CSV format with the following columns:

  dimension - The dimension in which the string is located (0=overworld,
              -1=nether, 1=the end).
  chunk_x   - The x-coordinate of the chunk containing the string.
  chunk_z   - The z-coordinate of the chunk containing the string.
  nbt_path  - The path within the NBT data tree where the string is located.
  value     - The string.

`
}

func (e *Extract) SetFlags(f *flag.FlagSet) {
	f.StringVar(&e.filter, "filter", "all", fmt.Sprintf("Only include entries matching a filter (one of: %s)", validOutputFilters()))
	f.BoolVar(&e.invert, "invert", false, "Output entries *not* matching the filter")
	f.BoolVar(&e.header, "header", true, "Include header row in the output")
	f.StringVar(&e.output, "output", "", "File to write results to (if empty, results are written to stdout)")
}

func (e *Extract) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if f.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "<world> is required.")
		return subcommands.ExitUsageError
	}
	if f.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "Extra positional arguments found.")
		return subcommands.ExitUsageError
	}
	e.world = f.Arg(0)
	of, ok := outputFilters[e.filter]
	if !ok {
		fmt.Fprintf(os.Stderr, "Invalid filter (%q), must be one of %s.\n", e.filter, validOutputFilters())
		return subcommands.ExitUsageError
	}
	if e.invert {
		orig := of
		of = func(k, v string) bool {
			return !orig(k, v)
		}
	}
	w := os.Stdout
	if e.output != "" {
		f, err := os.Create(e.output)
		if err != nil {
			log.Fatalf("Cannot open file %q for writing: %v", err)
		}
		defer f.Close()
		w = f
	}
	e.csv = csv.NewWriter(w)
	e.keep = of
	if e.header {
		e.csv.Write([]string{"dimension", "chunk_x", "chunk_z", "nbt_path", "value"})
	}
	if err := e.readWorld(e.world); err != nil {
		fmt.Fprintf(os.Stderr, "Cannot read world: %v\n", err)
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}
