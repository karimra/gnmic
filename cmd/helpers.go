package cmd

import (
	"os"
	"strings"

	"github.com/manifoldco/promptui"
)

func selectFromList(lsName string, items []string, initialPos, pageSize int) (int, string, error) {
	if pageSize <= 0 {
		pageSize = 10
	}
	p := promptui.Select{
		Label:        lsName,
		Items:        items,
		Size:         pageSize,
		CursorPos:    initialPos,
		Stdout:       os.Stderr,
		HideSelected: true,
	}

	pos, selected, err := p.Run()
	if err != nil {
		return 0, "", err
	}
	return pos, selected, nil
}

func selectManyFromList(lsName string, items []string, pageSize int) ([]string, error) {
	result := make([]string, 0)
	choice := ""
	var err error
	pos := 0
	nl := append([]string{".."}, items...)
	p := promptui.Select{
		Label:        lsName,
		Items:        nl,
		Size:         pageSize,
		CursorPos:    pos,
		Stdout:       os.Stdout,
		HideSelected: false,
		Searcher: func(input string, index int) bool {
			return strings.Contains(nl[index], input)
		},
		Keys: &promptui.SelectKeys{
			Prev:     promptui.Key{Code: promptui.KeyPrev, Display: promptui.KeyPrevDisplay},
			Next:     promptui.Key{Code: promptui.KeyNext, Display: promptui.KeyNextDisplay},
			PageUp:   promptui.Key{Code: promptui.KeyBackward, Display: promptui.KeyBackwardDisplay},
			PageDown: promptui.Key{Code: promptui.KeyForward, Display: promptui.KeyForwardDisplay},
			Search:   promptui.Key{Code: ':', Display: ":"},
		},
	}
CHOICE:
	pos, choice, err = p.Run()
	if err != nil {
		return nil, err
	}
	if choice == ".." {
		return result, nil
	}
	p.CursorPos = pos
	for _, r := range result {
		if r == choice {
			goto CHOICE
		}
	}
	result = append(result, choice)
	goto CHOICE
}
