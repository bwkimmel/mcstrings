Minecraft Strings
=================

This is a tool to manipulate strings in a Minecraft world.

## Installing

  - Install Go, following the instructions at https://golang.org/doc/install.
  - Run the following command:

```shell
go install github.com/bwkimmel/mcstrings@latest
```

NOTE: See https://golang.org/ref/mod#go-install for details on where this
installs executables. You may need to add this directory to your `PATH`.
Alternatively, use `git` to clone this repository and use `go run .` in place
of `mcstrings` in all of the commands below.

## Commands

For a list of commands, run:

```
mcstrings help
```

For help on a specific command, run:

```
mcstrings help <command>
```

### Extract

The `extract` command outputs strings to a CSV file. See [Strings File
Format](#strings-file-format) below.

  `mcstrings extract [<flags>...] <world>`

  - `<world>` (required): The path to the world (i.e., the directory containing
    `level.dat`).
  - `-filter`: Include only specific entries. One of:
    - `all`: Output all strings.
    - `user_text`: User-generated strings (e.g., signs, books, renamed items,
      etc.).
  - `-invert`: Include only entries *not* matching the filter.
  - `-header`: Include a header row in the output.
  - `-output`: The file to write results to. If not specified, results are
                written to stdout.

### Patch

    WARNING: This command will modify your world in-place. You should make a
    backup of your world before proceeding.

The `patch` command patches strings from a CSV file into a Minecraft world.
See [Strings File Format](#strings-file-format) below. Strings in the world that
are not present in the CSV file are left unmodified.

  `mcstrings patch -strings <csv_file> <world>`

  - `<world>` (required): The path to the world (i.e., the directory containing
    `level.dat`).
  - `-strings` (required): The path to the CSV file to patch into the world.

### Compact

    WARNING: This command will modify your world in-place. You should make a
    backup of your world before proceeding.

The `compact` command removes unused 4kB sectors from a Minecraft world. The
region files for a world contain 4kB sectors. The first 4kB of the file contains
a lookup table indicating in which sectors to find the data for each chunk. It
is therefore possible for there to be sectors that are not referenced in the
lookup table. These orphaned sectors could contain stale data. The `compact`
command removes this data and shrinks the region files accordingly. See [Region
file format](https://minecraft.gamepedia.com/wiki/Region_file_format).

  `mcstrings compact <world>`

  - `<world>` (required): The path to the world (i.e., the directory containing
    `level.dat`).

## Strings File Format

The strings are written as a CSV file having the following columns:

  - `dimension`: indicates the dimension containing the string. One of:
    -  0: Overworld
    - -1: Nether
    -  1: The End
  - `chunk_x`, `chunk_z`: The coordinates of the chunk in which the string is
    located.
  - `nbt_path`: The path in the NBT tree for that chunk that contains the string.
  - `value`: The string.

## Use Case: removing private user-generated text from a world

    WARNING: These instructions will modify your world in-place. You should make a
    backup of your world before proceeding.

Suppose you have a world you want to distribute (located in `/path/to/world` in
this example), but it contains private information that you would like to remove
before doing so.

NOTE: The following instructions only modify text in the *world* (e.g, signs,
renamed mobs, books or renamed items in chests or dropped on the ground, etc.).
It does not affect player data (e.g., items in player inventories or ender
chests). For this, you should remove the contents of the `playerdata` directory
from your world.

First, extract the user generated text from your world:
  
```shell
mcstrings extract -filter user_text -output strings.csv /path/to/world
```

This should capture everything that might be user-generated. If you wish, you
can double-check that nothing of interest was missed by listing all of the
strings that were omitted by the above command:

```shell
mcstrings extract -filter user_text -invert /path/to/world
```

Import `strings.csv` into your spreadsheet program of choice, or edit the file
by hand if you prefer. Edit the contents of the `value` column to your liking:
either blanking out values or redacting just the information you wish to hide.

NOTE: Some strings contain serialized JSON (e.g., sign text will appear as
`{"text":"A line of text"}`). If modifying these, it is important that the
modified text is still valid JSON. Sign text (which can be identified by one of
`Text1` through `Text4` at the end of the NBT path) may be blanked out entirely
without damaging the sign.

Export your changes as a CSV file (e.g., `redacted.csv`). Then patch your
changes back into the world:

```shell
mcstrings patch -strings redacted.csv /path/to/world
```

This command may tell you that chunks were resized or relocated, and recommend
that you run the [compact](#compact) command. This command removes orphaned
sectors from your world's region files, which may contain stale data. It is a
good idea to run this even if the `patch` command does not tell you to do so:

```shell
mcstrings compact /path/to/world
```

Open the world up in Minecraft to verify that your changes have been applied,
and/or re-run the extract tool:

```shell
mcstrings extract -filter user_text /path/to/world
```
