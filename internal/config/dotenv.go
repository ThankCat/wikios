package config

import (
	"errors"
	"os"

	"github.com/joho/godotenv"
)

func LoadDotEnv(paths ...string) error {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := loadSingleDotEnv(path); err != nil {
			return err
		}
	}
	return nil
}

func loadSingleDotEnv(path string) error {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	values, err := godotenv.Read(path)
	if err != nil {
		return err
	}
	for key, value := range values {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	return nil
}
