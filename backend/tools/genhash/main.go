package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/bcrypt"
)

func main() {
	password := flag.String("password", "", "Password to hash")
	flag.Parse()

	if *password == "" {
		*password = os.Getenv("HSM_PASSWORD")
	}
	if *password == "" {
		log.Fatal("Password is required (use -password or set HSM_PASSWORD)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(*password), 12)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(hash))
}
