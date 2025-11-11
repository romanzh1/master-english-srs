package handler

import (
	"context"
	"fmt"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/yourusername/master-english-srs/internal/models"
	"github.com/yourusername/master-english-srs/pkg/onenote"
	"go.uber.org/zap"
)

type Service interface {
	RegisterUser(ctx context.Context, telegramID int64, username, level string) error
	GetUser(ctx context.Context, telegramID int64) (*models.User, error)
	UserExists(ctx context.Context, telegramID int64) (bool, error)
	UpdateUserLevel(ctx context.Context, telegramID int64, level string) error
	GetAuthURL(telegramID int64) string
	ExchangeAuthCode(ctx context.Context, telegramID int64, code string) error
	GetOneNoteNotebooks(ctx context.Context, telegramID int64) ([]onenote.Notebook, error)
	GetOneNoteSections(ctx context.Context, telegramID int64, notebookID string) ([]onenote.Section, error)
	SaveOneNoteConfig(ctx context.Context, telegramID int64, notebookID, sectionID string) error
	SyncPages(ctx context.Context, telegramID int64) (int, error)
	GetDuePagesToday(ctx context.Context, telegramID int64) ([]*models.PageWithProgress, error)
	GetUserPages(ctx context.Context, telegramID int64) ([]*models.PageReference, error)
	GetPageContent(ctx context.Context, telegramID int64, pageID string) (string, error)
	UpdateReviewProgress(ctx context.Context, telegramID int64, pageID string, success bool) error
	GetAllUsersForReminders(ctx context.Context) ([]*models.User, error)
	GetProgress(ctx context.Context, telegramID int64, pageID string) (*models.UserProgress, error)
}

type TelegramHandler struct {
	api     *tgbotapi.BotAPI
	service Service
}

func NewTelegramHandler(token string, service Service) (*TelegramHandler, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, fmt.Errorf("create bot API: %w", err)
	}

	return &TelegramHandler{
		api:     api,
		service: service,
	}, nil
}

func (h *TelegramHandler) handleCommand(ctx context.Context, update tgbotapi.Update) {
	switch update.Message.Command() {
	case "start":
		h.handleStart(ctx, update)
	case "connect_onenote":
		h.handleConnectOneNote(ctx, update)
	case "sync":
		h.handleSync(ctx, update)
	case "today":
		h.handleToday(ctx, update)
	case "pages":
		h.handlePages(ctx, update)
	case "help":
		h.handleHelp(ctx, update)
	default:
		h.sendMessage(update.Message.Chat.ID, "Неизвестная команда. Используй /help")
	}
}

func (h *TelegramHandler) Start() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := h.api.GetUpdatesChan(u)

	zap.S().Info("bot started")

	go h.startReminderScheduler()

	for update := range updates {
		if update.Message == nil && update.CallbackQuery == nil {
			continue
		}

		h.handleUpdate(update)
	}
}

func (h *TelegramHandler) handleUpdate(update tgbotapi.Update) {
	ctx := context.Background()

	if update.Message != nil && update.Message.IsCommand() {
		h.handleCommand(ctx, update)
	} else if update.CallbackQuery != nil {
		h.handleCallback(ctx, update)
	}
}

func (h *TelegramHandler) handleStart(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	username := update.Message.From.UserName

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(update.Message.Chat.ID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if exists {
		h.sendMessage(update.Message.Chat.ID, "С возвращением! Используй /today для начала занятий.")
		return
	}

	text := `Привет! 👋

		Я помогу тебе изучать английский по системе интервальных повторений (SRS).
		
		Выбери свой уровень:`

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("A1", "level_A1"),
			tgbotapi.NewInlineKeyboardButtonData("A2", "level_A2"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("B1", "level_B1"),
			tgbotapi.NewInlineKeyboardButtonData("B2", "level_B2"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("C1", "level_C1"),
		),
	)

	h.sendMessageWithKeyboard(update.Message.Chat.ID, text, keyboard)

	if err := h.service.RegisterUser(ctx, userID, username, "B1"); err != nil {
		zap.S().Error("register user", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("username", username))
	}

	h.sendMessage(update.Message.Chat.ID, "Супер! Я запомнил.")
}

func (h *TelegramHandler) handleConnectOneNote(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID

	authURL := h.service.GetAuthURL(userID)

	text := fmt.Sprintf("Для подключения OneNote перейди по ссылке:\n\n%s\n\nПосле авторизации отправь мне полученный код.", authURL)
	h.sendMessage(update.Message.Chat.ID, text)
}

func (h *TelegramHandler) handleSync(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID

	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(update.Message.Chat.ID, "Сначала зарегистрируйся с помощью /start")
		return
	}

	if user.OneNoteAuth == nil {
		h.sendMessage(update.Message.Chat.ID, "Сначала подключи OneNote с помощью /connect_onenote")
		return
	}

	if user.OneNoteConfig == nil {
		h.sendMessage(update.Message.Chat.ID, "Настройка OneNote не завершена. Используй /connect_onenote")
		return
	}

	h.sendMessage(update.Message.Chat.ID, "Синхронизирую страницы...")

	count, err := h.service.SyncPages(ctx, userID)
	if err != nil {
		zap.S().Error("sync pages", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(update.Message.Chat.ID, "Не удалось синхронизировать страницы.")
		return
	}

	h.sendMessage(update.Message.Chat.ID, fmt.Sprintf("✅ Синхронизировано %d страниц", count))
}

func (h *TelegramHandler) handleToday(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID

	duePages, err := h.service.GetDuePagesToday(ctx, userID)
	if err != nil {
		zap.S().Error("get due pages today", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(update.Message.Chat.ID, "Произошла ошибка.")
		return
	}

	if len(duePages) == 0 {
		h.sendMessage(update.Message.Chat.ID, "🎉 Сегодня нет страниц для повторения!")
		return
	}

	text := "📚 Сегодня на повторение:\n\n"
	var buttons [][]tgbotapi.InlineKeyboardButton

	for i, pwp := range duePages {
		daysSince := int(time.Since(pwp.Progress.LastReviewDate).Hours() / 24)
		if pwp.Progress.RepetitionCount == 0 {
			text += fmt.Sprintf("%d. Страница #%d: \"%s\"\n   📅 Новая страница\n   📊 Прогресс: %d повторений\n\n",
				i+1, pwp.Page.PageNumber, pwp.Page.Title, pwp.Progress.RepetitionCount)
		} else {
			text += fmt.Sprintf("%d. Страница #%d: \"%s\"\n   📅 Последнее повторение: %d дней назад\n   📊 Прогресс: %d повторений\n\n",
				i+1, pwp.Page.PageNumber, pwp.Page.Title, daysSince, pwp.Progress.RepetitionCount)
		}

		button := tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("Показать страницу %d", i+1),
			fmt.Sprintf("show_%s", pwp.Page.PageID),
		)
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(button))
	}

	buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Пропустить всё", "skip_all"),
	))

	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons...)
	h.sendMessageWithKeyboard(update.Message.Chat.ID, text, keyboard)
}

func (h *TelegramHandler) handlePages(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID

	pages, err := h.service.GetUserPages(ctx, userID)
	if err != nil || len(pages) == 0 {
		h.sendMessage(update.Message.Chat.ID, "У тебя пока нет страниц. Используй /sync для синхронизации.")
		return
	}

	text := "📖 Твои страницы:\n\n"
	for _, page := range pages {
		progress, err := h.service.GetProgress(ctx, userID, page.PageID)
		if err != nil {
			zap.S().Error("get progress", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", page.PageID))
			continue
		}

		text += fmt.Sprintf("%d. %s\n   Повторений: %d | Интервал: %d дней\n\n",
			page.PageNumber, page.Title, progress.RepetitionCount, progress.IntervalDays)
	}

	h.sendMessage(update.Message.Chat.ID, text)
}

func (h *TelegramHandler) handleHelp(ctx context.Context, update tgbotapi.Update) {
	text := `📚 *Master English SRS*

		Доступные команды:
		
		/start - Начать работу с ботом
		/connect_onenote - Подключить OneNote
		/sync - Синхронизировать страницы из OneNote
		/today - Показать страницы на сегодня
		/pages - Список всех страниц
		/help - Справка`

	h.sendMessage(update.Message.Chat.ID, text)
}

func (h *TelegramHandler) handleCallback(ctx context.Context, update tgbotapi.Update) {
	callback := update.CallbackQuery
	data := callback.Data

	if strings.HasPrefix(data, "level_") {
		h.handleLevelSelection(ctx, callback)
	} else if strings.HasPrefix(data, "show_") {
		h.handleShowPage(ctx, callback)
	} else if strings.HasPrefix(data, "success_") {
		h.handleReviewSuccess(ctx, callback)
	} else if strings.HasPrefix(data, "failure_") {
		h.handleReviewFailure(ctx, callback)
	} else if data == "skip_all" {
		h.handleSkipAll(ctx, callback)
	}

	h.api.Send(tgbotapi.NewCallback(callback.ID, ""))
}

func (h *TelegramHandler) handleLevelSelection(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	level := strings.TrimPrefix(callback.Data, "level_")

	if err := h.service.UpdateUserLevel(ctx, callback.From.ID, level); err != nil {
		zap.S().Error("update user level", zap.Error(err), zap.Int64("telegram_id", callback.From.ID), zap.String("level", level))
		return
	}

	text := fmt.Sprintf("✅ Уровень установлен: %s\n\nТеперь подключи OneNote с помощью /connect_onenote", level)
	h.sendMessage(callback.Message.Chat.ID, text)
}

func (h *TelegramHandler) handleShowPage(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	pageID := strings.TrimPrefix(callback.Data, "show_")

	/*	page, err := h.service.GetUser(ctx, callback.From.ID)
		if err != nil {
			return
		}*/

	content, err := h.service.GetPageContent(ctx, callback.From.ID, pageID)
	if err != nil {
		zap.S().Error("get page content", zap.Error(err), zap.Int64("telegram_id", callback.From.ID), zap.String("page_id", pageID))
		h.sendMessage(callback.Message.Chat.ID, "Не удалось получить содержимое страницы.")
		return
	}

	text := fmt.Sprintf("📄 *Страница*\n\n━━━━━━━━━━━━━━━━━━━━━━\n\n%s\n\n━━━━━━━━━━━━━━━━━━━━━━\n\n", content)
	text += "💡 Скопируй эту страницу и отправь в бота Poe для генерации задания.\n\n"
	text += "После прохождения задания отметь результат:"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Успешно (≥80%)", fmt.Sprintf("success_%s", pageID)),
			tgbotapi.NewInlineKeyboardButtonData("⚠️ Неудача (<80%)", fmt.Sprintf("failure_%s", pageID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("↩️ Пропустить", "skip_all"),
		),
	)

	h.sendMessageWithKeyboard(callback.Message.Chat.ID, text, keyboard)
}

func (h *TelegramHandler) handleReviewSuccess(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	pageID := strings.TrimPrefix(callback.Data, "success_")
	h.updateReviewProgress(ctx, callback.From.ID, callback.Message.Chat.ID, pageID, true)
}

func (h *TelegramHandler) handleReviewFailure(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	pageID := strings.TrimPrefix(callback.Data, "failure_")
	h.updateReviewProgress(ctx, callback.From.ID, callback.Message.Chat.ID, pageID, false)
}

func (h *TelegramHandler) updateReviewProgress(ctx context.Context, userID int64, chatID int64, pageID string, success bool) {
	if err := h.service.UpdateReviewProgress(ctx, userID, pageID, success); err != nil {
		zap.S().Error("update review progress", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", pageID), zap.Bool("success", success))
		h.sendMessage(chatID, "Ошибка при обновлении прогресса.")
		return
	}

	progress, _ := h.service.GetProgress(ctx, userID, pageID)

	var statusText string
	if success {
		statusText = fmt.Sprintf("✅ Отлично! Следующее повторение через %d дней.", progress.IntervalDays)
	} else {
		statusText = "⚠️ Ничего страшного! Повторим завтра."
	}

	h.sendMessage(chatID, statusText)
}

func (h *TelegramHandler) handleSkipAll(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	h.sendMessage(callback.Message.Chat.ID, "Хорошо, пропускаем на сегодня. Увидимся завтра! 👋")
}

func (h *TelegramHandler) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	h.api.Send(msg)
}

func (h *TelegramHandler) sendMessageWithKeyboard(chatID int64, text string, keyboard interface{}) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	h.api.Send(msg)
}

func (h *TelegramHandler) startReminderScheduler() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		h.checkAndSendReminders()
	}
}

func (h *TelegramHandler) checkAndSendReminders() {
	ctx := context.Background()

	users, err := h.service.GetAllUsersForReminders(ctx)
	if err != nil {
		zap.S().Error("get all users for reminders", zap.Error(err))
		return
	}

	for _, user := range users {
		duePages, err := h.service.GetDuePagesToday(ctx, user.TelegramID)
		if err != nil {
			zap.S().Error("get due pages for reminder", zap.Error(err), zap.Int64("telegram_id", user.TelegramID))
			continue
		}

		if len(duePages) > 0 {
			text := h.formatReminderMessage(len(duePages))
			h.sendMessage(user.TelegramID, text)
		}
	}
}

func (h *TelegramHandler) formatReminderMessage(count int) string {
	var pageWord string
	if count == 1 {
		pageWord = "страница"
	} else if count >= 2 && count <= 4 {
		pageWord = "страницы"
	} else {
		pageWord = "страниц"
	}

	return fmt.Sprintf("🔔 Доброе утро! У тебя %d %s на повторение сегодня.\nИспользуй /today для начала.", count, pageWord)
}
