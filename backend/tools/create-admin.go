//go:build tools_legacy
// +build tools_legacy

package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func main() {
	// Open database
	db, err := sql.Open("sqlite", "./data/hytale-manager.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// Generate password hash
	password := os.Getenv("HSM_ADMIN_PASSWORD")
	if password == "" {
		log.Fatal("HSM_ADMIN_PASSWORD must be set")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		log.Fatal(err)
	}

	// Delete existing admin user
	_, err = db.Exec("DELETE FROM users WHERE username = 'admin'")
	if err != nil {
		log.Fatal(err)
	}

	// Insert admin user
	result, err := db.Exec(`
		INSERT INTO users (username, email, password_hash, is_active)
		VALUES (?, ?, ?, 1)
	`, "admin", "admin@example.com", string(hash))
	if err != nil {
		log.Fatal(err)
	}

	userID, _ := result.LastInsertId()

	// Assign Admin role (role ID 1)
	_, err = db.Exec(`
		INSERT INTO user_roles (user_id, role_id)
		VALUES (?, 1)
	`, userID)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Admin user created successfully!\n")
	fmt.Printf("Username: admin\n")
	fmt.Printf("\nIMPORTANT: Change this password after first login!\n")
}
