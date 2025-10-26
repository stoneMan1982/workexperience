package main

import (
	"fmt"
	"log"

	"github.com/stoneMan1982/workexperience/practice/golang/db"
)

func main() {
	fmt.Println("Hello, World!")

	d, err := db.OpenFromEnv()
	if err != nil {
		log.Printf("failed to open db: %v", err)
		return
	}
	defer d.Close()

	fmt.Println("DB connected (ping succeeded)")
}
