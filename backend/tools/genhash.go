//go:build tools_legacy
// +build tools_legacy

package main

import (
	"fmt"
	"log"
	"os"
	
	"golang.org/x/crypto/bcrypt"
)

func main() {
	password := os.Getenv("HSM_PASSWORD")
	if password == "" {
		log.Fatal("HSM_PASSWORD must be set")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(hash))
}
