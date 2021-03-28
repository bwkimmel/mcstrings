// Package commands provides the subcommands supported by this tool.
package commands

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/bwkimmel/mcstrings/log"
)

// confirm asks the user for confirmation before proceeding. If the user
// declines or provides an invalid response, the program will exit.
func confirm() {
	fmt.Print(`WARNING: This will modify your world in-place. You should make a backup before proceeding.

Proceed? (y/N): `)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		log.Info("Exiting.")
		os.Exit(1)
	}
	resp := scanner.Text()
	switch strings.TrimSpace(strings.ToLower(resp)) {
	case "y", "yes":
		return
	case "n", "no", "":
		log.Info("Exiting.")
		os.Exit(1)
	default:
		log.Errorf("Invalid response: %q, expected Y or N. Exiting.", resp)
		os.Exit(1)
	}
}
