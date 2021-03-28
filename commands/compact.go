package commands

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bwkimmel/mcstrings/log"
	"github.com/google/subcommands"
)

// Compact implements the compact command.
type Compact struct {
	skipConfirm bool
}

func (*Compact) Name() string {
	return "compact"
}

func (*Compact) Synopsis() string {
	return "Compact removes unused sectors from a Minecraft world."
}

func (*Compact) Usage() string {
	return `compact <world>
Compact removes unused sectors from a Minecraft world.

WARNING: This command will modify your world in-place. You should make a backup
of your world before proceeding.

Compact removes unused 4kB sectors from a Minecraft world. The region files for
a world contain 4kB sectors. The first 4kB of the file contains a lookup table
indicating in which sectors to find the data for each chunk. It is therefore
possible for there to be sectors that are not referenced in the lookup table.
These orphaned sectors could contain stale data. The compact command removes
this data and shrinks the region files accordingly. See 
https://minecraft.gamepedia.com/wiki/Region_file_format.

`
}

func (c *Compact) SetFlags(f *flag.FlagSet) {
	f.BoolVar(&c.skipConfirm, "skip_confirmation", false, "Do not ask for confirmation before proceeding.")
}

func (c *Compact) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if f.NArg() == 0 {
		log.Errorf("<world> is required.")
		return subcommands.ExitUsageError
	}
	if f.NArg() > 1 {
		log.Error("Extra positional arguments found.")
		return subcommands.ExitUsageError
	}
	if !c.skipConfirm {
		confirm()
	}
	if err := compactWorld(f.Arg(0)); err != nil {
		log.Errorf("Compact: %v", err)
		return subcommands.ExitFailure
	}
	return subcommands.ExitSuccess
}

// compactWorld compacts all region files in a world.
func compactWorld(path string) error {
	if err := compactDimension(filepath.Join(path, "region")); err != nil {
		return err
	}
	if err := compactDimension(filepath.Join(path, "DIM-1", "region")); err != nil {
		return err
	}
	if err := compactDimension(filepath.Join(path, "DIM1", "region")); err != nil {
		return err
	}
	return nil
}

// compactDimension compacts all region files in a dimension.
func compactDimension(path string) error {
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
		if err := compactRegion(region); err != nil {
			return fmt.Errorf("region file %q: %v", region, err)
		}
	}
	return nil
}

// compactRegion file compacts the specified region file.
func compactRegion(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("cannot open file: %v", err)
	}
	defer f.Close()

	// Read the chunk locations from the first 4kB of the file.
	locs := make([]uint32, 1024)
	if err := binary.Read(f, binary.BigEndian, locs); err != nil {
		return fmt.Errorf("cannot read chunk locations: %v", err)
	}

	// sectors lists the occupied 4kB sectors in the file. The first two 4kB
	// sectors are always occupied -- they contain the chunk location data and
	// chunk timestamps. See
	// https://minecraft.gamepedia.com/wiki/Region_file_format#Structure
	sectors := []int32{0, 1}

	// reloc maps original sectors to their new location. It will only be
	// populated for sectors which are the starts of chunk data.
	reloc := make(map[int32]int32)
	for _, loc := range locs {
		if loc == 0 {
			continue
		}
		start := int32((loc & 0xffffff00) >> 8)
		end := start + int32(loc&0xff)
		reloc[start] = -1 // Add placeholder for now.
		for sector := start; sector < end; sector++ {
			sectors = append(sectors, sector)
		}
	}

	// After sorting the list of occupied sectors, the index into this array will
	// represent the sector index after compaction, and the value will represent
	// the original sector index.
	sort.Slice(sectors, func(i, j int) bool {
		return sectors[i] < sectors[j]
	})

	// Sanity check: if a sector appears more than once, then there are
	// overlapping sectors in the file.
	prev := int32(-1)
	for _, sector := range sectors {
		if sector == prev {
			return fmt.Errorf("found overlapping sectors in region file")
		}
		prev = sector
	}

	buf := make([]byte, 4096)   // Buffer for transferring sector data.
	for i, j := range sectors { // i = new sector, j = old sector
		if _, ok := reloc[j]; ok { // Check for placeholder.
			reloc[j] = int32(i)
		}
		if int32(i) > j {
			return fmt.Errorf("cannot relocate sector later in file")
		} else if int32(i) == j {
			continue // No relocation necessary for this sector.
		}
		if _, err := f.Seek(int64(j)*4096, 0); err != nil {
			return fmt.Errorf("cannot seek to sector %d: %v", j, err)
		}
		if n, err := f.Read(buf); err != nil {
			return fmt.Errorf("cannot read sector %d: %v", j, err)
		} else if n != 4096 {
			return fmt.Errorf("sector %d: invalid length: %d", j, n)
		}
		if _, err := f.Seek(int64(i)*4096, 0); err != nil {
			return fmt.Errorf("cannot seek to sector %d: %v", i, err)
		}
		if _, err := f.Write(buf); err != nil {
			return fmt.Errorf("cannot write sector %d: %v", i, err)
		}
	}

	// Rebuild the chunk location table and write the updated table back to the
	// first 4kB of the file.
	for i, loc := range locs {
		if loc == 0 {
			continue
		}
		start := int32((loc & 0xffffff00) >> 8)
		count := int32(loc & 0xff)
		newStart, ok := reloc[start]
		if !ok {
			return fmt.Errorf("cannot find new location for sector %d", start)
		}
		locs[i] = uint32(newStart<<8) | uint32(count)
	}

	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("cannot seek to start of file: %v", err)
	}
	if err := binary.Write(f, binary.BigEndian, locs); err != nil {
		return fmt.Errorf("cannot write new chunk locations: %v", err)
	}

	// Truncate the now-unoccupied end of the file to its new length after
	// compaction.
	oldSize := int64(sectors[len(sectors)-1]) * 4096
	newSize := int64(len(sectors)-1) * 4096
	logLevel := log.Debugf
	if newSize < oldSize {
		logLevel = log.Infof
	}
	logLevel("Removing %d bytes from region file %q.", oldSize-newSize, path)
	if err := f.Truncate(newSize); err != nil {
		return fmt.Errorf("cannot truncate region file: %v", err)
	}
	return nil
}
