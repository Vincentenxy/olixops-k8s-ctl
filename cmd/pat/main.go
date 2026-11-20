// Platform personal tokens are managed in the platform's personal settings.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "Local PAT issuance has been retired; generate and revoke platform PATs in the platform's personal settings")
	os.Exit(1)
}
