//go:build windows

package roles

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type SelectorItem struct {
	Name        string
	Description string
	Group       string
	Tag         string
	Tooltip     string
	Checked     bool
}

var ErrCancelled = errors.New("selection cancelled")

func RunSelector(title string, items []SelectorItem, subtitle ...string) ([]string, error) {
	if len(items) == 0 {
		return nil, nil
	}

	fmt.Println(title)
	if len(subtitle) > 0 {
		fmt.Println(subtitle[0])
	}
	fmt.Println()

	for i, item := range items {
		check := " "
		if item.Checked {
			check = "*"
		}
		desc := ""
		if item.Description != "" {
			desc = " - " + item.Description
		}
		fmt.Printf("  [%s] %d. %s%s\n", check, i+1, item.Name, desc)
	}

	fmt.Println()
	fmt.Println("Enter numbers to toggle (e.g. 1,3,5), 'a' for all, or 'q' to cancel:")
	fmt.Print("> ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil, ErrCancelled
	}
	input := strings.TrimSpace(scanner.Text())

	if input == "" || input == "q" {
		return nil, ErrCancelled
	}

	selected := make(map[int]bool)
	for i, item := range items {
		if item.Checked {
			selected[i] = true
		}
	}

	if input == "a" {
		for i := range items {
			selected[i] = true
		}
	} else {
		for _, tok := range strings.Split(input, ",") {
			tok = strings.TrimSpace(tok)
			n, err := strconv.Atoi(tok)
			if err != nil || n < 1 || n > len(items) {
				continue
			}
			idx := n - 1
			if selected[idx] {
				delete(selected, idx)
			} else {
				selected[idx] = true
			}
		}
	}

	var result []string
	for i, item := range items {
		if selected[i] {
			result = append(result, item.Name)
		}
	}
	return result, nil
}
