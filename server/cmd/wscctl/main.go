package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"webscreencapture/server/internal/app"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "hash-password" {
		fmt.Fprintln(os.Stderr, "usage: wscctl hash-password < password.txt")
		os.Exit(2)
	}

	password, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && len(password) == 0 {
		fmt.Fprintln(os.Stderr, "read password:", err)
		os.Exit(1)
	}
	password = strings.TrimRight(password, "\r\n")
	if len(password) < 12 {
		fmt.Fprintln(os.Stderr, "password must be at least 12 characters")
		os.Exit(1)
	}

	hash, err := app.HashPassword(password)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash password:", err)
		os.Exit(1)
	}
	fmt.Println(hash)
}
