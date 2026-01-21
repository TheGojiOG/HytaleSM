package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

func main() {
	username := flag.String("username", "admin", "Username to create or promote")
	email := flag.String("email", "admin@example.com", "Email for new user")
	password := flag.String("password", "", "Password for new user")
	dbPath := flag.String("db", "./data/hytale-manager.db", "Path to SQLite database")
	flag.Parse()

	if *password == "" {
		*password = os.Getenv("HSM_ADMIN_PASSWORD")
	}
	if *password == "" {
		log.Fatal("Password is required (use -password or set HSM_ADMIN_PASSWORD)")
	}

	// Open database
	db, err := sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	var existingID int64
	err = db.QueryRow("SELECT id FROM users WHERE username = ?", *username).Scan(&existingID)
	if err == nil {
		assignAdminRoles(db, existingID)
		fmt.Printf("User %s promoted to Admin + ReleaseManager roles.\n", *username)
		return
	}

	// Generate password hash
	hash, err := bcrypt.GenerateFromPassword([]byte(*password), 12)
	if err != nil {
		log.Fatal(err)
	}

	// Insert admin user
	result, err := db.Exec(`
		INSERT INTO users (username, email, password_hash, is_active)
		VALUES (?, ?, ?, 1)
	`, *username, *email, string(hash))
	if err != nil {
		log.Fatal(err)
	}

	userID, _ := result.LastInsertId()

	assignAdminRoles(db, userID)

	fmt.Printf("Admin user created successfully!\n")
	fmt.Printf("Username: %s\n", *username)
	fmt.Printf("\nIMPORTANT: Change this password after first login!\n")
}

func assignAdminRoles(db *sql.DB, userID int64) {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO user_roles (user_id, role_id)
		SELECT ?, id FROM roles WHERE name IN ('Admin', 'ReleaseManager')
	`, userID)
	if err != nil {
		log.Fatal(err)
	}
}
