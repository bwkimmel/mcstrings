MC Strings
==========

This is a tool to manipulate strings in a Minecraft world.

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

## Commands

### Extract

The `extract` command outputs strings to a CSV file as described above.

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

The `patch` command patches strings from a CSV file as described above into a
Minecraft world. Strings in the world that are not present in the CSV file are
left unmodified.

  `mcstrings patch -strings <csv_file> <world>`

  - `<world>` (required): The path to the world (i.e., the directory containing
    `level.dat`).
  - `-strings` (required): The path to the CSV file to patch into the world.
