package main

import (
	"fmt"
	"log"
	"os"

	"github.com/joho/godotenv"
	"github.com/yourusername/master-english-srs/internal/handler"
	"github.com/yourusername/master-english-srs/internal/repository"
	"github.com/yourusername/master-english-srs/internal/service"
	"github.com/yourusername/master-english-srs/pkg/onenote"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	postgresHost := os.Getenv("POSTGRES_HOST")
	postgresPort := os.Getenv("POSTGRES_PORT")
	postgresUser := os.Getenv("POSTGRES_USER")
	postgresPassword := os.Getenv("POSTGRES_PASSWORD")
	postgresDB := os.Getenv("POSTGRES_DB")
	azureClientID := os.Getenv("AZURE_CLIENT_ID")
	azureClientSecret := os.Getenv("AZURE_CLIENT_SECRET")
	azureRedirectURI := os.Getenv("AZURE_REDIRECT_URI")

	if telegramToken == "" || postgresHost == "" {
		log.Fatal("Missing required environment variables")
	}

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		postgresHost, postgresPort, postgresUser, postgresPassword, postgresDB)

	repo, err := repository.NewDB(dsn, 10, 20)
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer repo.Close()

	if err = repo.Up("migrations"); err != nil {
		log.Fatalf("Could not reload migrations: %v", err)
	}

	scopes := []string{"Notes.Read", "offline_access"}
	authService := onenote.NewAuthService(azureClientID, azureClientSecret, azureRedirectURI, scopes)
	oneNoteClient := onenote.NewClient()

	svc := service.NewService(repo, authService, oneNoteClient)

	bot, err := handler.NewTelegramHandler(telegramToken, svc)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	bot.Start()
}
