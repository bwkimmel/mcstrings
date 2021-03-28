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
type Compact struct{}

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

func (*Compact) SetFlags(*flag.FlagSet) {}

func (*Compact) Execute(_ context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if f.NArg() == 0 {
		log.Errorf("<world> is required.")
		return subcommands.ExitUsageError
	}
	if f.NArg() > 1 {
		log.Error("Extra positional arguments found.")
		return subcommands.ExitUsageError
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

	locs := make([]uint32, 1024)
	sectors := []int32{0, 1}
	reloc := make(map[int32]int32)
	for i := 0; i < 1024; i++ {
		if err := binary.Read(f, binary.BigEndian, &locs[i]); err != nil {
			return fmt.Errorf("cannot read chunk location: %v", err)
		}
		if locs[i] == 0 {
			continue
		}
		start := int32((locs[i] & 0xffffff00) >> 8)
		end := start + int32(locs[i]&0xff)
		reloc[start] = -1
		for sector := start; sector < end; sector++ {
			sectors = append(sectors, sector)
		}
	}

	sort.Slice(sectors, func(i, j int) bool {
		return sectors[i] < sectors[j]
	})

	buf := make([]byte, 4096)
	for i, j := range sectors {
		if _, ok := reloc[j]; ok {
			reloc[j] = int32(i)
		}
		if int32(i) > j {
			return fmt.Errorf("cannot relocate sector later in file")
		} else if int32(i) == j {
			continue
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

	if _, err := f.Seek(0, 0); err != nil {
		return fmt.Errorf("cannot seek to start of file: %v", err)
	}
	for _, loc := range locs {
		if loc != 0 {
			start := int32((loc & 0xffffff00) >> 8)
			count := int32(loc & 0xff)
			newStart, ok := reloc[start]
			if !ok {
				return fmt.Errorf("cannot find new location for sector %d", start)
			}
			loc = uint32(newStart<<8) | uint32(count)
		}
		if err := binary.Write(f, binary.BigEndian, loc); err != nil {
			return fmt.Errorf("cannot write new chunk location: %v", err)
		}
	}

	oldSize := int64(sectors[len(sectors)-1]) * 4096
	newSize := int64(len(sectors)-1) * 4096
	log.Debugf("Removing %d bytes from region file %q.", oldSize-newSize, path)
	if err := f.Truncate(newSize); err != nil {
		return fmt.Errorf("cannot truncate region file: %v", err)
	}
	return nil
}
