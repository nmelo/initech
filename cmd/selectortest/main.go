// selectortest is a manual QA harness for the role selector widget.
// Run: go run ./cmd/selectortest
package main

import (
	"fmt"
	"os"

	"github.com/nmelo/initech/internal/roles"
)

func main() {
	items := []roles.SelectorItem{
		{Name: "super", Description: "Dispatcher and coordinator", Group: "COORDINATORS", Tag: "supervised"},
		{Name: "eng1", Description: "Autonomous engineer #1", Group: "ENGINEERS", Tag: "needs src"},
		{Name: "eng2", Description: "Autonomous engineer #2", Group: "ENGINEERS", Tag: "needs src"},
		{Name: "eng3", Description: "Autonomous engineer #3", Group: "ENGINEERS", Tag: "needs src"},
		{Name: "qa1", Description: "QA verification agent #1", Group: "QA", Tag: "needs src"},
		{Name: "qa2", Description: "QA verification agent #2", Group: "QA", Tag: "needs src"},
		{Name: "shipper", Description: "Release and deployment", Group: "SHIPPING", Tag: "supervised"},
		{Name: "pm", Description: "Product management", Group: "PRODUCT"},
		{Name: "pmm", Description: "Product marketing", Group: "PRODUCT"},
		{Name: "arch", Description: "Architecture and design", Group: "SPECIALISTS"},
		{Name: "sec", Description: "Security review", Group: "SPECIALISTS"},
		{Name: "writer", Description: "Documentation and content", Group: "SPECIALISTS"},
		{Name: "ops", Description: "Operations and infrastructure", Group: "SPECIALISTS"},
		{Name: "growth", Description: "Growth and analytics", Group: "SPECIALISTS", Tag: "needs src"},
	}

	selected, err := roles.RunSelector("Select agents for my-project", items)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nCancelled: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nSelected %d roles:\n", len(selected))
	for _, name := range selected {
		fmt.Println(" ", name)
	}
}
