// mcstrings is a tool for extracting strings from a Minecraft world.
package main

import (
	"context"
	"flag"
	"os"

	"github.com/bwkimmel/mcstrings/commands"
	"github.com/google/subcommands"
)

func main() {
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")
	subcommands.Register(&commands.Compact{}, "")
	subcommands.Register(&commands.Extract{}, "")
	subcommands.Register(&commands.Patch{}, "")

	flag.Parse()
	ctx := context.Background()
	os.Exit(int(subcommands.Execute(ctx)))
}
