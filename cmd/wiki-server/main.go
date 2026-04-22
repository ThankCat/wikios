package main

import (
	"log"
	"os"

	"wikios/internal/app"
	"wikios/internal/config"
)

func main() {
	if err := config.LoadDotEnv(".env", ".env.local"); err != nil {
		log.Fatalf("load .env: %v", err)
	}

	cfg, err := config.Load(os.Getenv("WIKIOS_CONFIG"))
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("build app: %v", err)
	}

	if err := application.Run(); err != nil {
		log.Fatalf("run app: %v", err)
	}
}
