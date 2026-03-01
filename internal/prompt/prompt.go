package prompt

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func Confirm(message string) (bool, error) {
	fmt.Printf("%s [y/N]: ", message)
	r := bufio.NewReader(os.Stdin)
	line, err := r.ReadString('\n')
	if err != nil {
		return false, err
	}
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes", nil
}
