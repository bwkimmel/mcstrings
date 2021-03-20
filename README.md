MC Strings
==========

This is a tool to extract strings from a Minecraft world.

## Output Format

The strings are written as a CSV file having the following columns:

  - `dimension`: indicates the dimension containing the string. One of:
    -  0: Overworld
    - -1: Nether
    -  1: The End
  - `chunk_x`, `chunk_z`: The coordinates of the chunk in which the string is
    located.
  - `nbt_path`: The path in the NBT tree for that chunk that contains the string.
  - `value`: The string.

## Options

  - `--world` (required): The path to the world (i.e., the directory containing
    `level.dat`).
  - `--filter`: Include only specific entries. One of:
    - `all`: Output all strings.
    - `user_text`: User-generated strings (e.g., signs, books, renamed items,
      etc.).
  - `--invert`: Include only entries *not* matching the filter.
  - `--header`: Include a header row in the output.
  - `--output`: The file to write results to. If not specified, results are
                written to stdout.
