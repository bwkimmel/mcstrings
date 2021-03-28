// mcstrings is a tool for extracting strings from a Minecraft world.
package main

import (
	"context"
	"flag"
	"os"

	"github.com/bwkimmel/mcstrings/commands"
	"github.com/bwkimmel/mcstrings/log"
	"github.com/google/subcommands"
)

var (
	verbose = flag.Bool("verbose", false, "Enable verbose logging output.")
	quiet   = flag.Bool("quiet", false, "Surpress most logging output.")
)

func main() {
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")
	subcommands.Register(&commands.Compact{}, "")
	subcommands.Register(&commands.Extract{}, "")
	subcommands.Register(&commands.Patch{}, "")

	flag.Parse()
	if *quiet && *verbose {
		log.Error("Cannot specify both --quiet and --verbose.")
	} else if *quiet {
		log.SetMinLevel(log.ErrorLevel)
	} else if *verbose {
		log.SetMinLevel(log.DebugLevel)
	}

	ctx := context.Background()
	os.Exit(int(subcommands.Execute(ctx)))
}
