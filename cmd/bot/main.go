package main

import (
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/romanzh1/master-english-srs/internal/handler"
	"github.com/romanzh1/master-english-srs/internal/repository"
	"github.com/romanzh1/master-english-srs/internal/service"
	"github.com/romanzh1/master-english-srs/pkg/onenote"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func main() {
	// Загружаем московскую временную зону
	moscowLocation, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		moscowLocation = time.UTC
		zap.S().Warn("failed to load Moscow location, using UTC", zap.Error(err))
	}

	config := zap.NewDevelopmentConfig()
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	config.EncoderConfig.TimeKey = "timestamp"
	// Исправление: используем кастомный encoder с московским временем
	config.EncoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		moscowTime := t.In(moscowLocation)
		enc.AppendString(moscowTime.Format("2006-01-02T15:04:05-07:00"))
	}

	logger, err := config.Build()
	if err != nil {
		panic(fmt.Errorf("init logger: %w", err))
	}
	defer logger.Sync()

	zap.ReplaceGlobals(logger)
	zap.S().Info("logger initialized")

	if err := godotenv.Load(); err != nil {
		zap.S().Debug("load .env file", zap.Error(err))
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
		zap.S().Fatal("missing required environment variables")
	}

	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		postgresHost, postgresPort, postgresUser, postgresPassword, postgresDB)

	repo, err := repository.NewDB(dsn, 10, 20)
	if err != nil {
		zap.S().Error("connect to PostgreSQL", zap.Error(err), zap.String("host", postgresHost))
		os.Exit(1)
	}
	defer repo.Close()

	if err = repo.Up("migrations"); err != nil {
		zap.S().Error("run migrations", zap.Error(err))
		os.Exit(1)
	}

	scopes := []string{"Notes.Read", "offline_access"}
	authService := onenote.NewAuthService(azureClientID, azureClientSecret, azureRedirectURI, scopes)
	oneNoteClient := onenote.NewClient()

	svc := service.NewService(repo, authService, oneNoteClient)

	bot, err := handler.NewTelegramHandler(telegramToken, svc)
	if err != nil {
		zap.S().Error("create telegram handler", zap.Error(err))
		os.Exit(1)
	}

	bot.Start()
}
