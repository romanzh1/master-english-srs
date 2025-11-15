package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/romanzh1/master-english-srs/internal/models"
	"github.com/romanzh1/master-english-srs/internal/service"
	"github.com/romanzh1/master-english-srs/pkg/onenote"
	"github.com/romanzh1/master-english-srs/pkg/utils"
	"go.uber.org/zap"
)

type Service interface {
	RegisterUser(ctx context.Context, telegramID int64, username, level string) error
	GetUser(ctx context.Context, telegramID int64) (*models.User, error)
	UserExists(ctx context.Context, telegramID int64) (bool, error)
	UpdateUserLevel(ctx context.Context, telegramID int64, level string) error
	GetAllUsersForReminders(ctx context.Context) ([]*models.User, error)

	GetAuthURL(telegramID int64) string
	ExchangeAuthCode(ctx context.Context, telegramID int64, code string) error

	GetOneNoteNotebooks(ctx context.Context, telegramID int64) ([]onenote.Notebook, error)
	GetOneNoteSections(ctx context.Context, telegramID int64, notebookID string) ([]onenote.Section, error)
	SaveOneNoteConfig(ctx context.Context, telegramID int64, notebookID, sectionID string) error

	GetDuePagesToday(ctx context.Context, telegramID int64) ([]*models.PageWithProgress, error)
	GetUserPagesInProgress(ctx context.Context, telegramID int64) ([]*models.PageReference, error)
	GetPageContent(ctx context.Context, telegramID int64, pageID string) (string, error)
	UpdateReviewProgress(ctx context.Context, telegramID int64, pageID string, grade int) error
	UpdateMaxPagesPerDay(ctx context.Context, telegramID int64, maxPages uint) error
	GetProgress(ctx context.Context, telegramID int64, pageID string) (*models.UserProgress, error)
	RunDailyCron(ctx context.Context) error
	PrepareMaterials(ctx context.Context, telegramID int64) error
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
	case "select_notebook":
		h.handleSelectNotebook(ctx, update)
	case "select_section":
		h.handleSelectSection(ctx, update)
	case "today":
		h.handleToday(ctx, update)
	case "pages":
		h.handlePages(ctx, update)
	case "set_max_pages":
		h.handleSetMaxPages(ctx, update)
	case "get_max_pages":
		h.handleGetMaxPages(ctx, update)
	case "prepare_materials":
		h.handlePrepareMaterials(ctx, update)
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
	go h.startDailyCron()

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
		// Проверяем, что сообщение от пользователя (не от канала или группы)
		if update.Message.From == nil {
			zap.S().Warn("received command from nil user")
			return
		}
		h.handleCommand(ctx, update)
	} else if update.Message != nil {
		// Проверяем, что сообщение от пользователя (не от канала или группы)
		if update.Message.From == nil {
			zap.S().Warn("received message from nil user")
			return
		}
		// Обрабатываем текстовые сообщения (например, код авторизации)
		h.handleTextMessage(ctx, update)
	} else if update.CallbackQuery != nil {
		// Проверяем, что callback от пользователя
		if update.CallbackQuery.From == nil {
			zap.S().Warn("received callback from nil user")
			return
		}
		h.handleCallback(ctx, update)
	}
}

func (h *TelegramHandler) handleStart(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID

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
}

func (h *TelegramHandler) handleConnectOneNote(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	authURL := h.service.GetAuthURL(userID)

	text := fmt.Sprintf("Для подключения OneNote перейди по ссылке:\n\n%s\n\nПосле авторизации отправь мне полученный код.", authURL)
	h.sendMessage(chatID, text)
}

func (h *TelegramHandler) handleSelectNotebook(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	// Проверяем, что пользователь авторизован
	if user.AccessToken == nil || user.RefreshToken == nil {
		h.sendMessage(chatID, "Сначала подключи OneNote с помощью команды /connect_onenote")
		return
	}

	notebooks, err := h.service.GetOneNoteNotebooks(ctx, userID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get notebooks", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось получить список книг OneNote. Попробуй позже.")
		return
	}

	if len(notebooks) == 0 {
		h.sendMessage(chatID, "У тебя нет доступных книг OneNote.")
		return
	}

	text := "📚 Выбери книгу OneNote для синхронизации:\n\n"
	var buttons [][]tgbotapi.InlineKeyboardButton

	for _, notebook := range notebooks {
		button := tgbotapi.NewInlineKeyboardButtonData(
			notebook.DisplayName,
			fmt.Sprintf("notebook_%s", notebook.ID),
		)
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(button))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons...)
	h.sendMessageWithKeyboard(chatID, text, keyboard)
}

func (h *TelegramHandler) handleSelectSection(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	// Проверяем, что пользователь авторизован
	if user.AccessToken == nil || user.RefreshToken == nil {
		h.sendMessage(chatID, "Сначала подключи OneNote с помощью команды /connect_onenote")
		return
	}

	// Проверяем, что выбран notebook
	if user.NotebookID == nil || *user.NotebookID == "" {
		h.sendMessage(chatID, "Сначала выбери книгу OneNote с помощью команды /select_notebook")
		return
	}

	sections, err := h.service.GetOneNoteSections(ctx, userID, *user.NotebookID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get sections", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось получить список секций OneNote. Попробуй позже.")
		return
	}

	if len(sections) == 0 {
		h.sendMessage(chatID, "В выбранной книге нет доступных секций.")
		return
	}

	text := "📑 Выбери секцию OneNote для синхронизации:\n\n"
	var buttons [][]tgbotapi.InlineKeyboardButton

	for _, section := range sections {
		button := tgbotapi.NewInlineKeyboardButtonData(
			section.DisplayName,
			fmt.Sprintf("section_%s", section.ID),
		)
		buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(button))
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons...)
	h.sendMessageWithKeyboard(chatID, text, keyboard)
}

func (h *TelegramHandler) handleTextMessage(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	text := strings.TrimSpace(update.Message.Text)
	chatID := update.Message.Chat.ID

	// Проверяем, может ли это быть код авторизации (длина от 20 до 200 символов)
	if len(text) < 20 || len(text) >= 200 {
		// Это не код авторизации - отправляем подсказку пользователю
		h.sendMessage(chatID, "Я не понимаю эту команду. Используй /help для списка доступных команд.")
		return
	}

	// Проверяем, существует ли пользователь
	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	// Проверяем, была ли уже авторизация
	wasAuthorized := user.AccessToken != nil && user.RefreshToken != nil

	// Пытаемся обменять код на токены (работает для новой и обновлённой авторизации)
	err = h.service.ExchangeAuthCode(ctx, userID, text)
	if err != nil {
		// Если пользователь не авторизован, показываем ошибку
		if !wasAuthorized {
			zap.S().Error("exchange auth code", zap.Error(err), zap.Int64("telegram_id", userID))
			h.sendMessage(chatID, "❌ Не удалось обработать код авторизации. Убедись, что код правильный и не истёк. Попробуй получить новый код через /connect_onenote")
		} else {
			// Если пользователь уже авторизован и код не подошёл, это не код авторизации
			h.sendMessage(chatID, "Не удалось обработать код. Убедись, что код правильный и не истёк. Попробуй получить новый код через /connect_onenote")
		}
		return
	}

	// После успешного обмена кода отправляем соответствующее сообщение
	if wasAuthorized {
		h.sendMessage(chatID, "✅ Авторизация обновлена!")
	} else {
		text := "✅ Авторизация успешна!\n\nТеперь выбери книгу OneNote с помощью /select_notebook, а затем секцию с помощью /select_section."
		h.sendMessage(chatID, text)
	}
}

// handleAuthError обрабатывает ошибку авторизации и отправляет пользователю сообщение с запросом повторной авторизации
func (h *TelegramHandler) handleAuthError(err error, userID, chatID int64) bool {
	authErr, ok := err.(*service.AuthRequiredError)
	if !ok {
		return false
	}

	zap.S().Warn("authentication required", zap.Int64("telegram_id", authErr.TelegramID))
	authURL := h.service.GetAuthURL(userID)
	text := fmt.Sprintf("❌ Требуется повторная авторизация. Твой токен истёк.\n\nПерейди по ссылке для авторизации:\n\n%s\n\nПосле авторизации отправь мне полученный код.", authURL)
	h.sendMessage(chatID, text)
	return true
}

func (h *TelegramHandler) handleToday(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	// Проверяем, существует ли пользователь
	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	duePages, err := h.service.GetDuePagesToday(ctx, userID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get due pages today", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка.")
		return
	}

	if len(duePages) == 0 {
		h.sendMessage(chatID, "🎉 Сегодня нет страниц для повторения!")
		return
	}

	text := "📚 Сегодня на повторение:\n\n"
	var buttons [][]tgbotapi.InlineKeyboardButton

	for i, pwp := range duePages {
		daysSince := int(time.Since(pwp.Progress.LastReviewDate).Hours() / 24)
		// Экранируем название страницы для безопасной вставки в HTML
		escapedTitle := escapeHTML(pwp.Page.Title)
		if pwp.Progress.RepetitionCount == 0 {
			text += fmt.Sprintf("%d. \"%s\"\n   📅 Новая страница\n   📊 Прогресс: %d повторений\n\n",
				i+1, escapedTitle, pwp.Progress.RepetitionCount)
		} else {
			text += fmt.Sprintf("%d. \"%s\"\n   📅 Последнее повторение: %d дней назад\n   📊 Прогресс: %d повторений\n\n",
				i+1, escapedTitle, daysSince, pwp.Progress.RepetitionCount)
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
	h.sendMessageWithKeyboard(chatID, text, keyboard)
}

func (h *TelegramHandler) handlePages(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	pages, err := h.service.GetUserPagesInProgress(ctx, userID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get user pages", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка.")
		return
	}

	if len(pages) == 0 {
		h.sendMessage(chatID, "У тебя пока нет страниц. Используй /connect_onenote для подключения OneNote.")
		return
	}

	text := "📖 Твои страницы:\n\n"
	for _, page := range pages {
		progress, err := h.service.GetProgress(ctx, userID, page.PageID)
		if err != nil {
			zap.S().Error("get progress", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", page.PageID))
			continue
		}

		// Экранируем название страницы для безопасной вставки в HTML
		escapedTitle := escapeHTML(page.Title)
		text += fmt.Sprintf("• %s\n   Повторений: %d | Интервал: %d дней\n\n",
			escapedTitle, progress.RepetitionCount, progress.IntervalDays)
	}

	h.sendMessage(chatID, text)
}

func (h *TelegramHandler) handleHelp(ctx context.Context, update tgbotapi.Update) {
	text := `📚 <b>Master English SRS</b>

		Доступные команды:

		/start - Начать работу с ботом
		/connect_onenote - Подключить OneNote
		/select_notebook - Выбрать книгу OneNote для синхронизации
		/select_section - Выбрать секцию OneNote для синхронизации
		/today - Показать страницы на сегодня
		/pages - Список всех страниц
		/set_max_pages - Установить максимальное количество страниц в день (2, 3 или 4)
		/get_max_pages - Показать текущее максимальное количество страниц в день
		/prepare_materials - Подгрузить дополнительный материал на сегодня
		/help - Справка

		Примечание: Страницы синхронизируются автоматически при запросе.`

	h.sendMessage(update.Message.Chat.ID, text)
}

func (h *TelegramHandler) handleCallback(ctx context.Context, update tgbotapi.Update) {
	callback := update.CallbackQuery
	data := callback.Data
	chatID := callback.Message.Chat.ID

	if strings.HasPrefix(data, "level_") {
		h.handleLevelSelection(ctx, callback)
	} else if strings.HasPrefix(data, "notebook_") {
		h.handleNotebookSelection(ctx, callback)
	} else if strings.HasPrefix(data, "section_") {
		h.handleSectionSelection(ctx, callback)
	} else if strings.HasPrefix(data, "show_") {
		h.handleShowPage(ctx, callback)
	} else if strings.HasPrefix(data, "grade_80_100_") {
		h.handleGradeReview(ctx, callback, 90) // Use 90 as middle value for 80-100 range
	} else if strings.HasPrefix(data, "grade_60_80_") {
		h.handleGradeReview(ctx, callback, 70) // Use 70 as middle value for 60-80 range
	} else if strings.HasPrefix(data, "grade_40_60_") {
		h.handleGradeReview(ctx, callback, 50) // Use 50 as middle value for 40-60 range
	} else if strings.HasPrefix(data, "grade_0_40_") {
		h.handleGradeReview(ctx, callback, 30) // Use 30 as middle value for 0-40 range
	} else if strings.HasPrefix(data, "success_") {
		// Legacy support - treat as easy (80+)
		h.handleGradeReview(ctx, callback, 90)
	} else if strings.HasPrefix(data, "failure_") {
		// Legacy support - treat as forgot (<40)
		h.handleGradeReview(ctx, callback, 30)
	} else if data == "skip_all" {
		h.handleSkipAll(ctx, callback)
	} else {
		// Неизвестный callback - отправляем уведомление пользователю
		zap.S().Warn("unknown callback data", zap.String("data", data), zap.Int64("user_id", callback.From.ID))
		h.sendMessage(chatID, "Неизвестная команда. Используй /help для списка доступных команд.")
	}

	// Всегда отвечаем на callback, чтобы убрать индикатор загрузки
	callbackConfig := tgbotapi.NewCallback(callback.ID, "")
	if _, err := h.api.Request(callbackConfig); err != nil {
		zap.S().Error("send callback answer", zap.Error(err), zap.String("callback_id", callback.ID))
	}
}

func (h *TelegramHandler) handleLevelSelection(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	username := callback.From.UserName
	level := strings.TrimPrefix(callback.Data, "level_")
	chatID := callback.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		// Регистрируем нового пользователя с выбранным уровнем
		if err := h.service.RegisterUser(ctx, userID, username, level); err != nil {
			zap.S().Error("register user", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("username", username), zap.String("level", level))
			h.sendMessage(chatID, "Произошла ошибка при регистрации. Попробуй позже.")
			return
		}
		text := fmt.Sprintf("✅ Регистрация завершена! Уровень установлен: %s\n\nТеперь подключи OneNote с помощью /connect_onenote", level)
		h.sendMessage(chatID, text)
	} else {
		// Обновляем уровень существующего пользователя
		if err := h.service.UpdateUserLevel(ctx, userID, level); err != nil {
			zap.S().Error("update user level", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("level", level))
			h.sendMessage(chatID, "Произошла ошибка при обновлении уровня. Попробуй позже.")
			return
		}
		text := fmt.Sprintf("✅ Уровень обновлён: %s\n\nТеперь подключи OneNote с помощью /connect_onenote", level)
		h.sendMessage(chatID, text)
	}
}

func (h *TelegramHandler) handleNotebookSelection(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	notebookID := strings.TrimPrefix(callback.Data, "notebook_")
	chatID := callback.Message.Chat.ID

	// Получаем пользователя, чтобы узнать текущий sectionID (если есть)
	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	// Сохраняем только notebookID, sectionID оставляем как есть (или nil, если его нет)
	sectionID := ""
	if user.SectionID != nil {
		sectionID = *user.SectionID
	}

	if err := h.service.SaveOneNoteConfig(ctx, userID, notebookID, sectionID); err != nil {
		zap.S().Error("save notebook config", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("notebook_id", notebookID))
		h.sendMessage(chatID, "Не удалось сохранить выбранную книгу. Попробуй позже.")
		return
	}

	text := "✅ Книга OneNote выбрана!\n\nТеперь выбери секцию с помощью команды /select_section"
	h.sendMessage(chatID, text)
}

func (h *TelegramHandler) handleSectionSelection(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	sectionID := strings.TrimPrefix(callback.Data, "section_")
	chatID := callback.Message.Chat.ID

	// Получаем пользователя, чтобы узнать текущий notebookID
	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	// Проверяем, что выбран notebook
	if user.NotebookID == nil || *user.NotebookID == "" {
		h.sendMessage(chatID, "Сначала выбери книгу OneNote с помощью команды /select_notebook")
		return
	}

	if err := h.service.SaveOneNoteConfig(ctx, userID, *user.NotebookID, sectionID); err != nil {
		zap.S().Error("save section config", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("section_id", sectionID))
		h.sendMessage(chatID, "Не удалось сохранить выбранную секцию. Попробуй позже.")
		return
	}

	text := "✅ Секция OneNote выбрана!\n\nТеперь OneNote настроен. Используй /today для начала занятий."
	h.sendMessage(chatID, text)
}

func (h *TelegramHandler) handleShowPage(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	pageID := strings.TrimPrefix(callback.Data, "show_")
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID

	content, err := h.service.GetPageContent(ctx, userID, pageID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get page content", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", pageID))
		h.sendMessage(chatID, "Не удалось получить содержимое страницы.")
		return
	}

	// Экранируем содержимое страницы для безопасной вставки в HTML
	escapedContent := escapeHTML(content)
	text := fmt.Sprintf("📄 <b>Страница</b>\n\n━━━━━━━━━━━━━━━━━━━━━━\n\n%s\n\n━━━━━━━━━━━━━━━━━━━━━━\n\n", escapedContent)
	text += "💡 Скопируй эту страницу и отправь в бота Poe для генерации задания.\n\n"
	text += "После прохождения задания отметь результат:"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Easy (>80%)", fmt.Sprintf("grade_80_100_%s", pageID)),
			tgbotapi.NewInlineKeyboardButtonData("🟢 Normal (>60%)", fmt.Sprintf("grade_60_80_%s", pageID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🟡 Hard (>40%)", fmt.Sprintf("grade_40_60_%s", pageID)),
			tgbotapi.NewInlineKeyboardButtonData("🔴 Forgot (<40%)", fmt.Sprintf("grade_0_40_%s", pageID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("↩️ Пропустить", "skip_all"),
		),
	)

	h.sendMessageWithKeyboard(callback.Message.Chat.ID, text, keyboard)
}

func (h *TelegramHandler) handleGradeReview(ctx context.Context, callback *tgbotapi.CallbackQuery, grade int) {
	// Extract pageID from callback data
	data := callback.Data
	var pageID string
	if strings.HasPrefix(data, "grade_80_100_") {
		pageID = strings.TrimPrefix(data, "grade_80_100_")
	} else if strings.HasPrefix(data, "grade_60_80_") {
		pageID = strings.TrimPrefix(data, "grade_60_80_")
	} else if strings.HasPrefix(data, "grade_40_60_") {
		pageID = strings.TrimPrefix(data, "grade_40_60_")
	} else if strings.HasPrefix(data, "grade_0_40_") {
		pageID = strings.TrimPrefix(data, "grade_0_40_")
	} else if strings.HasPrefix(data, "success_") {
		pageID = strings.TrimPrefix(data, "success_")
	} else if strings.HasPrefix(data, "failure_") {
		pageID = strings.TrimPrefix(data, "failure_")
	} else {
		zap.S().Warn("unknown grade callback format", zap.String("data", data))
		return
	}

	h.updateReviewProgress(ctx, callback.From.ID, callback.Message.Chat.ID, pageID, grade)
}

func (h *TelegramHandler) handleReviewSuccess(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	// Legacy support
	h.handleGradeReview(ctx, callback, 90)
}

func (h *TelegramHandler) handleReviewFailure(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	// Legacy support
	h.handleGradeReview(ctx, callback, 30)
}

func (h *TelegramHandler) updateReviewProgress(ctx context.Context, userID int64, chatID int64, pageID string, grade int) {
	if err := h.service.UpdateReviewProgress(ctx, userID, pageID, grade); err != nil {
		zap.S().Error("update review progress", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", pageID), zap.Int("grade", grade))
		h.sendMessage(chatID, "Ошибка при обновлении прогресса.")
		return
	}

	progress, _ := h.service.GetProgress(ctx, userID, pageID)

	var statusText string
	switch {
	case grade > 80:
		statusText = fmt.Sprintf("✅ Easy! Следующее повторение через %d дней.", progress.IntervalDays)
	case grade > 60:
		statusText = fmt.Sprintf("🟢 Normal! Следующее повторение через %d дней.", progress.IntervalDays)
	case grade > 40:
		statusText = fmt.Sprintf("🟡 Hard! Следующее повторение через %d дней.", progress.IntervalDays)
	default:
		statusText = "🔴 Forgot! Повторим завтра."
	}

	h.sendMessage(chatID, statusText)
}

func (h *TelegramHandler) handleSkipAll(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	h.sendMessage(callback.Message.Chat.ID, "Хорошо, пропускаем на сегодня. Увидимся завтра! 👋")
}

func (h *TelegramHandler) handleSetMaxPages(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	// Parse number from message text after command
	parts := strings.Fields(update.Message.Text)
	if len(parts) < 2 {
		h.sendMessage(chatID, "Использование: /set_max_pages <число>\n\nНапример: /set_max_pages 3\n\nДоступные значения: 2, 3 или 4")
		return
	}

	maxPagesInt, err := strconv.Atoi(parts[1])
	if err != nil || maxPagesInt < 2 || maxPagesInt > 4 {
		h.sendMessage(chatID, "Некорректное значение. Используй число от 2 до 4.\n\n2 страницы в день → добавляется 1 страница\n3 страницы в день → добавляется 1 (60%) или 2 (40%)\n4 страницы в день → добавляется 2 страницы")
		return
	}

	maxPages := uint(maxPagesInt)
	if err := h.service.UpdateMaxPagesPerDay(ctx, userID, maxPages); err != nil {
		zap.S().Error("update max pages per day", zap.Error(err), zap.Int64("telegram_id", userID), zap.Uint("max_pages", maxPages))
		h.sendMessage(chatID, "Ошибка при обновлении настроек. Попробуй позже.")
		return
	}

	h.sendMessage(chatID, fmt.Sprintf("✅ Максимальное количество страниц в день установлено: %d", maxPages))
}

func (h *TelegramHandler) handleGetMaxPages(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	maxPages := uint(2) // default
	if user.MaxPagesPerDay != nil {
		maxPages = *user.MaxPagesPerDay
	}

	h.sendMessage(chatID, fmt.Sprintf("📊 Текущее максимальное количество страниц в день: %d", maxPages))
}

func (h *TelegramHandler) handlePrepareMaterials(ctx context.Context, update tgbotapi.Update) {
	userID := update.Message.From.ID
	chatID := update.Message.Chat.ID

	exists, err := h.service.UserExists(ctx, userID)
	if err != nil {
		zap.S().Error("check user exists", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if !exists {
		h.sendMessage(chatID, "Сначала зарегистрируйся с помощью команды /start")
		return
	}

	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	if user.AccessToken == nil || user.RefreshToken == nil {
		h.sendMessage(chatID, "Сначала подключи OneNote с помощью команды /connect_onenote")
		return
	}

	if user.NotebookID == nil || *user.NotebookID == "" {
		h.sendMessage(chatID, "Сначала выбери книгу OneNote с помощью команды /select_notebook")
		return
	}

	warningMsg := "⚠️ Внимание! Эта команда добавляет материалы для повторения.\n" +
		"Не рекомендуется использовать её часто, иначе материалы будут накапливаться и в будущем придётся повторять слишком много за один день.\n\n" +
		"Обычно материалы подготавливаются автоматически в 00:00 каждый день.\n\n" +
		"Подготавливаю материалы..."

	h.sendMessage(chatID, warningMsg)

	if err := h.service.PrepareMaterials(ctx, userID); err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("prepare materials", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось подготовить материалы. Попробуй позже.")
		return
	}

	h.sendMessage(chatID, "✅ Материалы успешно подготовлены!")
}

func (h *TelegramHandler) startDailyCron() {
	zone, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		zap.S().Error("load moscow location", zap.Error(err))
		zone = time.UTC
	}

	getNextMidnight := func() time.Time {
		now := utils.TruncateToMinutes(time.Now().In(zone))
		today := utils.StartOfDay(now)

		return today.AddDate(0, 0, 1)
	}

	nextRun := getNextMidnight()
	timer := time.NewTimer(time.Until(nextRun))

	var lastRunDate time.Time

	for {
		<-timer.C

		now := utils.TruncateToMinutes(time.Now().In(zone)) //TODO rewrite zone to method
		currentDate := utils.StartOfDay(now)

		if now.Hour() == 0 && now.Minute() == 0 && !utils.DatesEqual(lastRunDate, currentDate) {
			ctx := context.Background()

			if err := h.service.RunDailyCron(ctx); err != nil {
				zap.S().Error("run daily cron", zap.Error(err))
			} else {
				lastRunDate = currentDate
				zap.S().Info("daily cron completed successfully")
			}
		}

		nextRun = getNextMidnight()
		timer.Reset(time.Until(nextRun))
	}
}

// escapeHTML экранирует специальные символы HTML для безопасной вставки в HTML-текст
func escapeHTML(text string) string {
	// Экранируем только три символа: &, <, >
	// Важно: сначала экранируем &, чтобы не экранировать уже экранированные символы
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}

func (h *TelegramHandler) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	// Используем HTML для форматирования текста (жирный шрифт через <b>текст</b>)
	msg.ParseMode = tgbotapi.ModeHTML
	if _, err := h.api.Send(msg); err != nil {
		zap.S().Error("send message", zap.Error(err), zap.Int64("chat_id", chatID))
	}
}

func (h *TelegramHandler) sendMessageWithKeyboard(chatID int64, text string, keyboard interface{}) {
	msg := tgbotapi.NewMessage(chatID, text)
	// Используем HTML для форматирования текста (жирный шрифт через <b>текст</b>)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = keyboard
	if _, err := h.api.Send(msg); err != nil {
		zap.S().Error("send message with keyboard", zap.Error(err), zap.Int64("chat_id", chatID))
	}
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
			// Если требуется авторизация, пропускаем этого пользователя (не отправляем уведомление)
			if _, ok := err.(*service.AuthRequiredError); ok {
				zap.S().Warn("authentication required for reminder", zap.Int64("telegram_id", user.TelegramID))
				continue
			}
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
