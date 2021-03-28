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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/bwkimmel/mcstrings/log"
	"github.com/google/subcommands"
	"github.com/sandertv/gophertunnel/minecraft/nbt"
)

var (
	dirRE = regexp.MustCompile(`^([^/\[]+)(?:\[(\d+)\])?$`)
	zeros = make([]byte, 4096)
)

// Patch implements the patch command.
type Patch struct {
	strings     string
	world       string
	csv         *csv.Reader
	chunk       *chunk
	skipConfirm bool

	// shouldCompact indicates whether any chunks required resizing or relocating.
	// If so, notify the user that they should compact the world.
	shouldCompact bool
}

type chunk struct {
	dim, x, z int
	nbt       map[string]interface{}
	updates   int
}

func (*Patch) Name() string {
	return "patch"
}

func (*Patch) Synopsis() string {
	return "Patch strings into a Minecraft world."
}

func (*Patch) Usage() string {
	return `patch -strings <csv_file> <world>
Patch strings into a Minecraft world.

WARNING: This command will modify your world in-place. You should make a backup
of your world before proceeding.

Patch strings from a CSV file into a Minecraft world located in the directory
<world>. This should be the directory containing level.dat. The CSV file should
have the same columns as generated by the "extract" command.

`
}

func (p *Patch) SetFlags(f *flag.FlagSet) {
	f.StringVar(&p.strings, "strings", "", "The CSV file to read strings from (required).")
	f.BoolVar(&p.skipConfirm, "skip_confirmation", false, "Do not ask for confirmation before proceeding.")
}

func (p *Patch) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if f.NArg() == 0 {
		log.Error("<world> is required.")
		return subcommands.ExitUsageError
	}
	if f.NArg() > 1 {
		log.Error("Extra positional arguments found.")
		return subcommands.ExitUsageError
	}
	p.world = f.Arg(0)
	if p.strings == "" {
		log.Error("--strings is required.")
		return subcommands.ExitUsageError
	}
	file, err := os.Open(p.strings)
	if err != nil {
		log.Errorf("Cannot open strings file: %v", err)
		return subcommands.ExitFailure
	}
	defer file.Close()
	if !p.skipConfirm {
		confirm()
	}
	p.csv = csv.NewReader(file)
	p.csv.FieldsPerRecord = -1 // Don't check the number of fields.
	if err := p.run(); err != nil {
		log.Errorf("Patch: %v", err)
		return subcommands.ExitFailure
	}
	if p.shouldCompact {
		log.Info("Some chunks were resized or relocated. It is recommended to compact the world.")
	}
	return subcommands.ExitSuccess
}

// field returns the nth string in an array, or "" if index is beyond the bounds
// of the array.
func field(rec []string, index int) string {
	if len(rec) <= index {
		return ""
	}
	return rec[index]
}

// patchString replaces the string at the specified NBT path in the currently
// loaded chunk with a new value.
func (p *Patch) patchString(path, value string) error {
	var node interface{} = p.chunk.nbt
	set := func() {}
	parts := strings.Split(path, "/")
	for i, part := range parts {
		component := dirRE.FindStringSubmatch(part)
		if component == nil {
			return fmt.Errorf("cannot parse nbt_path")
		}
		compound, ok := node.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s is not a TAG_Compound", strings.Join(parts[:i], "/"))
		}
		elem, ok := compound[component[1]]
		if !ok {
			return fmt.Errorf("cannot find %s", strings.Join(append(parts[:i], component[1]), "/"))
		}
		set = func() { compound[component[1]] = value }
		node = elem
		if len(component) < 3 || component[2] == "" { // No array index.
			continue
		}
		index, err := strconv.Atoi(component[2])
		if err != nil {
			return fmt.Errorf("invalid index in nbt_path: %v", err)
		}
		array, ok := node.([]interface{})
		if !ok {
			return fmt.Errorf("%s is not a TAG_List", strings.Join(append(parts[:i], component[1]), "/"))
		}
		if index < 0 || index >= len(array) {
			return fmt.Errorf("index %d out of bounds; %s has length %d", index, strings.Join(append(parts[:i], component[1]), "/"), len(array))
		}
		set = func() { array[index] = value }
		node = array[index]
	}
	oldValue, ok := node.(string)
	if !ok {
		return fmt.Errorf("%s is not a TAG_String", path)
	}
	if oldValue != value {
		p.chunk.updates++
		set()
	}
	return nil
}

// run patches the Minecraft world.
func (p *Patch) run() error {
	line := 0
	for {
		line++
		rec, err := p.csv.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if line == 1 && field(rec, 0) == "dimension" {
			continue // Skip header row if present.
		}
		ok := true
		warn := func(msg string, args ...interface{}) {
			args = append([]interface{}{line}, args...)
			log.Warnf("Line %d: "+msg, args...)
			ok = false
		}
		dim, err := strconv.Atoi(field(rec, 0))
		if err != nil {
			warn("invalid dimension: %v", err)
		}
		x, err := strconv.Atoi(field(rec, 1))
		if err != nil {
			warn("invalid chunk_x: %v", err)
		}
		z, err := strconv.Atoi(field(rec, 2))
		if err != nil {
			warn("invalid chunk_z: %v", err)
		}
		path := field(rec, 3)
		if path == "" {
			warn("missing nbt_path")
		}
		if !ok {
			continue
		}
		if err := p.loadChunk(dim, x, z); err != nil {
			return err
		}
		if err := p.patchString(path, field(rec, 4)); err != nil {
			return fmt.Errorf("line %d, dimension %d, chunk (%d, %d): %v", line, dim, x, z, err)
		}
	}
	return p.saveChunk()
}

// dimensionPath returns the directory containing the region files for the
// specified dimension.
func (p *Patch) dimensionPath(dim int) (string, error) {
	switch dim {
	case 0:
		return filepath.Join(p.world, "region"), nil
	case 1:
		return filepath.Join(p.world, "DIM1", "region"), nil
	case -1:
		return filepath.Join(p.world, "DIM-1", "region"), nil
	default:
		return "", fmt.Errorf("invalid dimension: %d", dim)
	}
}

// regionPath returns the path to the file containing the data for the specified
// region.
func (p *Patch) regionPath(dim, rx, rz int) (string, error) {
	dimPath, err := p.dimensionPath(dim)
	if err != nil {
		return "", err
	}
	return filepath.Join(dimPath, fmt.Sprintf("r.%d.%d.mca", rx, rz)), nil
}

// chunkPos returns the region x-z coordinates, and chunk offset offset x-z
// coordinates within the region.
func chunkPos(x, z int) (rx, rz, dx, dz int) {
	rx, rz = x/32, z/32
	dx, dz = x%32, z%32
	if dx < 0 {
		rx--
		dx += 32
	}
	if dz < 0 {
		rz--
		dz += 32
	}
	return rx, rz, dx, dz
}

// loadChunk loads the specified chunk. If the specified chunk is already
// loaded, no action is taken. If it is not, the currently-loaded chunk (if
// there is one) is saved to disk and the new chunk is loaded.
func (p *Patch) loadChunk(dim, x, z int) error {
	// If we already had a different chunk loaded, save it before loading the new
	// chunk.
	if p.chunk != nil && p.chunk.dim == dim && p.chunk.x == x && p.chunk.z == z {
		return nil
	}
	if err := p.saveChunk(); err != nil {
		return err
	}
	rx, rz, dx, dz := chunkPos(x, z)
	regPath, err := p.regionPath(dim, rx, rz)
	if err != nil {
		return err
	}
	log.Debugf("Loading dimension %d, chunk (%d, %d) from %q.", dim, x, z, regPath)
	f, err := os.Open(regPath)
	if err != nil {
		return fmt.Errorf("cannot open region file %q for reading: %v", regPath, err)
	}
	defer f.Close()
	// Find where the chunk data is located within the file. See
	// https://minecraft.gamepedia.com/wiki/Region_file_format#Chunk_location.
	if _, err := f.Seek(int64(4*(dz*32+dx)), 0); err != nil {
		return fmt.Errorf("cannot find location of chunk (%d, %d) in %q: %v", x, z, regPath, err)
	}
	var loc uint32
	if err := binary.Read(f, binary.BigEndian, &loc); err != nil {
		return fmt.Errorf("cannot read location of chunk (%d, %d) in %q: %v", x, z, regPath, err)
	}
	offset := int64(4096 * (loc & 0xffffff00) >> 8)
	size := int64(4096 * (loc & 0xff))
	if _, err := f.Seek(offset, 0); err != nil {
		return fmt.Errorf("cannot seek to chunk (%d, %d) in %q: %v", x, z, regPath, err)
	}
	nbt, err := readChunk(&io.LimitedReader{f, size})
	if err != nil {
		return fmt.Errorf("cannot read chunk (%d, %d) in %q: %v", x, z, regPath, err)
	}
	p.chunk = &chunk{dim: dim, x: x, z: z, nbt: nbt}
	return nil
}

// nopWriteCloser adapts an io.Writer to provide a no-op Close() method.
type nopWriteCloser struct {
	io.Writer
}

// Close implements io.WriteCloser.
func (*nopWriteCloser) Close() error {
	return nil
}

// wrapWriter wraps a writer to apply the specified compression algorithm. See
// https://minecraft.gamepedia.com/Region_file_format#Chunk_data for valid
// compression algorithms.
func wrapWriter(w io.Writer, compression int8) (io.WriteCloser, error) {
	switch compression {
	case 1:
		return gzip.NewWriter(w), nil
	case 2:
		return zlib.NewWriter(w), nil
	case 3:
		return &nopWriteCloser{w}, nil
	default:
		return nil, fmt.Errorf("invalid compression type: %d", compression)
	}
}

// saveChunk saves the currently-loaded chunk to disk if there is a chunk that
// is loaded and if it is dirty.
func (p *Patch) saveChunk() (err error) {
	// There is nothing to do if there is no loaded chunk or if the loaded chunk
	// has no updates.
	if p.chunk == nil || p.chunk.updates == 0 {
		return nil
	}
	dim, x, z := p.chunk.dim, p.chunk.x, p.chunk.z
	rx, rz, dx, dz := chunkPos(x, z)
	regPath, err := p.regionPath(dim, rx, rz)
	if err != nil {
		return err
	}
	log.Debugf("Saving dimension %d, chunk (%d, %d) to %q with %d updates.", dim, x, z, regPath, p.chunk.updates)
	defer func() {
		if err != nil {
			err = fmt.Errorf("saving chunk (%d, %d) to %q: %v", x, z, regPath, err)
		}
	}()
	f, err := os.OpenFile(regPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("cannot open region file %q for writing: %v", err)
	}
	defer f.Close()
	// Find the location in the region file of the currently-loaded chunk. See
	// https://minecraft.gamepedia.com/wiki/Region_file_format#Chunk_location.
	if _, err := f.Seek(int64(4*(dz*32+dx)), 0); err != nil {
		return fmt.Errorf("cannot find chunk location: %v", err)
	}
	var loc uint32
	if err := binary.Read(f, binary.BigEndian, &loc); err != nil {
		return fmt.Errorf("cannot read chunk location: %v", err)
	}
	offset := int64(4096 * (loc & 0xffffff00) >> 8)
	sectors := int32(loc & 0xff)
	if _, err := f.Seek(offset, 0); err != nil {
		return fmt.Errorf("cannot seek to chunk: %v", err)
	}
	// Read the chunk header, which includes a 4-byte length and a 1-byte
	// compression type. See
	// https://minecraft.gamepedia.com/wiki/Region_file_format#Chunk_data.
	var (
		length      int32
		compression int8
	)
	if err := binary.Read(f, binary.BigEndian, &length); err != nil {
		return fmt.Errorf("cannot read length of chunk: %v", err)
	}
	if err := binary.Read(f, binary.BigEndian, &compression); err != nil {
		return fmt.Errorf("cannot read compression type: %v", err)
	}
	var buf bytes.Buffer
	w, err := wrapWriter(&buf, compression)
	if err != nil {
		return err
	}
	enc := nbt.NewEncoderWithEncoding(w, nbt.BigEndian)
	if err := enc.Encode(p.chunk.nbt); err != nil {
		return fmt.Errorf("cannot encode NBT data: %v", err)
	}
	w.Close()
	// The length field in the chunk data includes the 1-byte compression type.
	// See https://minecraft.gamepedia.com/wiki/Region_file_format#Chunk_data.
	length = int32(buf.Len() + 1)
	// Sector count includes the 4-byte length, the 1-byte compression type, and
	// the compressed data.
	newSectors := (length + 4) / 4096
	if (length+4)%4096 != 0 { // Round up to next whole sector.
		newSectors++
	}
	// Check of the new sector count will fit in one byte.
	if newSectors > 255 {
		return fmt.Errorf("new chunk data is too large (%d sectors)", newSectors)
	}
	// If we require more 4kB sectors than the original chunk data occupied, don't
	// assume we can expand into the next sector in the file. Instead, relocate
	// the chunk to the end of the file (exception: don't relocate if the sector
	// is already located at the end of the file).
	if newSectors > sectors {
		end, err := f.Seek(0, 2)
		if err != nil {
			return fmt.Errorf("could not seek to end of region file: %v", err)
		}
		if end%4096 != 0 {
			return fmt.Errorf("region file is invalid: not a multiple of 4kB")
		}
		// If this is not already the last chunk in the file, relocate the chunk to
		// the end of the file.
		if offset+int64(sectors)*4096 < end {
			log.Debugf("Relocating dimension %d, chunk (%d, %d) from %d to end of file at %d.", dim, x, z, offset, end)
			offset = end
		}
	}
	// If the sector count has changed, we need to go back to the chunk location
	// table to write the new sector count (and possibly new offset if we've
	// relocated the sector).
	if newSectors != sectors {
		log.Debugf("Resizing dimension %d, chunk (%d, %d) to from %d sectors to %d sectors.", dim, x, z, sectors, newSectors)
		p.shouldCompact = true // Advise user to run compaction when we're done.
		if _, err := f.Seek(int64(4*(dz*32+dx)), 0); err != nil {
			return fmt.Errorf("cannot find chunk location: %v", err)
		}
		loc = uint32((offset/4096)<<8) | uint32(newSectors)
		if err := binary.Write(f, binary.BigEndian, loc); err != nil {
			return fmt.Errorf("cannot write new location for chunk (%d, %d) in %q: %v", x, z, regPath, err)
		}
	}
	// Seek back to the chunk data.
	if _, err := f.Seek(offset, 0); err != nil {
		return fmt.Errorf("cannot seek to chunk: %v", err)
	}
	if err := binary.Write(f, binary.BigEndian, length); err != nil {
		return fmt.Errorf("cannot write length: %v", err)
	}
	if _, err := f.Seek(1, 1); err != nil { // Skip over compression type.
		return err
	}
	if _, err := io.Copy(f, &buf); err != nil {
		return fmt.Errorf("could not write NBT data: %v", err)
	}
	// Pad with zeros to the end of this 4kB sector.
	// See https://minecraft.gamepedia.com/wiki/Region_file_format#Chunk_data.
	pos, err := f.Seek(0, 1) // Get current position.
	if err != nil {
		return err
	}
	if partial := pos % 4096; partial != 0 {
		if _, err := io.CopyN(f, bytes.NewReader(zeros), 4096-partial); err != nil {
			return fmt.Errorf("could not write padding: %v", err)
		}
	}
	return nil
}
