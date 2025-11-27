package handler

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/romanzh1/master-english-srs/internal/models"
	"github.com/romanzh1/master-english-srs/internal/service"
	"github.com/romanzh1/master-english-srs/pkg/utils"
	"go.uber.org/zap"
)

type TelegramHandler struct {
	api     *tgbotapi.BotAPI
	service models.Service
}

func NewTelegramHandler(token string, service models.Service) (*TelegramHandler, error) {
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
	case "set_timezone":
		h.handleSetTimezone(ctx, update)
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

	for i, notebook := range notebooks {
		callbackData := fmt.Sprintf("notebook_%d", i)
		button := tgbotapi.NewInlineKeyboardButtonData(
			notebook.DisplayName,
			callbackData,
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

	for i, section := range sections {
		callbackData := fmt.Sprintf("section_%d", i)
		button := tgbotapi.NewInlineKeyboardButtonData(
			section.DisplayName,
			callbackData,
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

	text := "📚 <b>Сегодня на повторение:</b>\n\n"
	var buttons [][]tgbotapi.InlineKeyboardButton
	counter := 0

	nowUTC := utils.NowUTC()
	for i, pwp := range duePages {
		daysSince := int(nowUTC.Sub(pwp.Progress.LastReviewDate).Hours() / 24)
		escapedTitle := escapeHTML(pwp.Page.Title)

		pageNumber := extractPageNumberFromTitle(pwp.Page.Title)
		shouldNumber := pageNumber == 999999

		var prefix string
		var buttonText string
		if shouldNumber {
			counter++
			prefix = fmt.Sprintf("%d. ", counter)
			buttonText = fmt.Sprintf("Показать страницу %d", counter)
		} else {
			prefix = ""
			buttonText = fmt.Sprintf("Показать страницу %d", pageNumber)
		}

		if pwp.Progress.RepetitionCount == 0 {
			text += fmt.Sprintf("%s%s\n   📅 Новая страница\n   📊 Прогресс: %d повторений\n\n",
				prefix, escapedTitle, pwp.Progress.RepetitionCount)
		} else {
			text += fmt.Sprintf("%s%s\n   📅 Последнее повторение: %d дней назад\n   📊 Прогресс: %d повторений\n\n",
				prefix, escapedTitle, daysSince, pwp.Progress.RepetitionCount)
		}

		callbackData := fmt.Sprintf("show_%d", i)
		button := tgbotapi.NewInlineKeyboardButtonData(buttonText, callbackData)
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

	user, err := h.service.GetUser(ctx, userID)
	if err != nil {
		zap.S().Error("get user", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка. Попробуй позже.")
		return
	}

	timezone := "UTC"
	if user.Timezone != nil && *user.Timezone != "" {
		timezone = *user.Timezone
	}

	pages, err := h.service.GetUserAllPagesInProgress(ctx, userID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get user pages", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Произошла ошибка.")
		return
	}

	if len(pages) == 0 {
		h.sendMessage(chatID, "У тебя пока нет страниц, приходи завтра или используй /prepare_materials.")
		return
	}

	text := "📖 <b>Твои страницы:</b>\n\n"
	counter := 0
	for _, page := range pages {
		progress, err := h.service.GetProgress(ctx, userID, page.PageID)
		if err != nil {
			zap.S().Error("get progress", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", page.PageID))
			continue
		}

		lastScore, err := h.service.GetLastReviewScore(ctx, userID, page.PageID)
		if err != nil {
			zap.S().Warn("get last review score", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", page.PageID))
			lastScore = 0
		}

		var scoreEmoji string
		if lastScore > 80 {
			scoreEmoji = "✅"
		} else if lastScore > 60 {
			scoreEmoji = "🟢"
		} else if lastScore >= 40 {
			scoreEmoji = "🟡"
		} else if lastScore > 0 {
			scoreEmoji = "🔴"
		} else {
			scoreEmoji = ""
		}

		escapedTitle := escapeHTML(page.Title)

		pageNumber := extractPageNumberFromTitle(page.Title)
		shouldNumber := pageNumber == 999999

		var prefix string
		if shouldNumber {
			counter++
			prefix = fmt.Sprintf("%d. ", counter)
		} else {
			prefix = ""
		}

		// Convert NextReviewDate to user's timezone for display
		nextReviewInTz, err := utils.ToUserTimezone(progress.NextReviewDate, timezone)
		if err != nil {
			zap.S().Warn("failed to convert next review date to user timezone", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("timezone", timezone))
			nextReviewInTz = progress.NextReviewDate
		}
		nextReviewStr := nextReviewInTz.Format("02.01.2006")

		reviewedTodayStr := ""
		if progress.ReviewedToday {
			reviewedTodayStr = " | ✅ Повторено сегодня"
		}

		var scoreStr string
		if lastScore > 0 {
			if scoreEmoji != "" {
				scoreStr = fmt.Sprintf(" | %s %d%%", scoreEmoji, lastScore)
			} else {
				scoreStr = fmt.Sprintf(" | %d%%", lastScore)
			}
		} else {
			scoreStr = ""
		}

		text += fmt.Sprintf("%s%s\n   📅 Следующее повторение: %s\n   📊 Прогресс: %d повторений%s%s\n\n",
			prefix, escapedTitle, nextReviewStr, progress.RepetitionCount, reviewedTodayStr, scoreStr)
	}

	h.sendMessage(chatID, text)
}

// extractPageNumberFromTitle извлекает первое число из начала заголовка страницы
// Например, "14 Grammar Sequence of Tenses" -> 14
// Если число не найдено, возвращает 999999 для индикации отсутствия номера
func extractPageNumberFromTitle(title string) int {
	title = strings.TrimSpace(title)
	if title == "" {
		return 0
	}

	// Ищем первое число в начале строки
	re := regexp.MustCompile(`^\d+`)
	match := re.FindString(title)
	if match == "" {
		return 999999
	}

	num, err := strconv.Atoi(match)
	if err != nil {
		return 999999
	}

	return num
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
		/set_max_pages - Установить максимальное количество страниц в день на повторение
		/get_max_pages - Показать текущее максимальное количество страниц в день для повторения
		/prepare_materials - Подгрузить дополнительную страницу на сегодня
		/set_timezone - Установить таймзону (например, /set_timezone Europe/Moscow)

		/help - Справка`

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
	} else if data == "skip_page" {
		h.handleSkipPage(ctx, callback)
	} else if data == "skip_all" {
		h.handleSkipAll(ctx, callback)
	} else if data == "start_today_yes" {
		h.handleStartTodayYes(ctx, callback)
	} else if data == "start_today_no" {
		h.handleStartTodayNo(ctx, callback)
	} else if strings.HasPrefix(data, "timezone_") {
		h.handleTimezoneSelection(ctx, callback)
	} else if strings.HasPrefix(data, "max_pages_") {
		h.handleMaxPagesSelection(ctx, callback)
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
		text := fmt.Sprintf("✅ Регистрация завершена! Уровень установлен: %s\n\nВыбери максимальное количество страниц в день для повторения:", level)
		h.sendMessage(chatID, text)
		// Показываем выбор max_pages после регистрации
		h.showMaxPagesSelector(chatID)
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
	indexStr := strings.TrimPrefix(callback.Data, "notebook_")
	chatID := callback.Message.Chat.ID

	// Получаем пользователя, чтобы узнать текущий sectionID (если есть)
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

	// Получаем список notebooks и находим нужный по индексу
	notebooks, err := h.service.GetOneNoteNotebooks(ctx, userID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get notebooks", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось получить список книг. Попробуй позже.")
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 || index >= len(notebooks) {
		zap.S().Error("invalid notebook index", zap.String("index", indexStr), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Неверный выбор. Попробуй заново через /select_notebook")
		return
	}

	notebookID := notebooks[index].ID

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
	indexStr := strings.TrimPrefix(callback.Data, "section_")
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

	// Проверяем, что пользователь авторизован
	if user.AccessToken == nil || user.RefreshToken == nil {
		h.sendMessage(chatID, "Сначала подключи OneNote с помощью команды /connect_onenote")
		return
	}

	// Получаем список sections и находим нужную по индексу
	sections, err := h.service.GetOneNoteSections(ctx, userID, *user.NotebookID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get sections", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось получить список секций. Попробуй позже.")
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 || index >= len(sections) {
		zap.S().Error("invalid section index", zap.String("index", indexStr), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Неверный выбор. Попробуй заново через /select_section")
		return
	}

	sectionID := sections[index].ID

	if err := h.service.SaveOneNoteConfig(ctx, userID, *user.NotebookID, sectionID); err != nil {
		zap.S().Error("save section config", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("section_id", sectionID))
		h.sendMessage(chatID, "Не удалось сохранить выбранную секцию. Попробуй позже.")
		return
	}

	text := "✅ Секция OneNote выбрана!\n\nТеперь OneNote настроен.\n\nХочешь начать повторять уже сегодня?"
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Да", "start_today_yes"),
			tgbotapi.NewInlineKeyboardButtonData("Нет", "start_today_no"),
		),
	)
	h.sendMessageWithKeyboard(chatID, text, keyboard)
}

func (h *TelegramHandler) handleShowPage(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	indexStr := strings.TrimPrefix(callback.Data, "show_")
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID

	// Получаем список страниц для повторения и находим нужную по индексу
	duePages, err := h.service.GetDuePagesToday(ctx, userID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get due pages today", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось получить список страниц. Попробуй заново через /today")
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 || index >= len(duePages) {
		zap.S().Error("invalid page index", zap.String("index", indexStr), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Неверный выбор. Попробуй заново через /today")
		return
	}

	pageID := duePages[index].Page.PageID

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

	// Проверяем режим: чтение (IntervalDays == 0) или AI (IntervalDays >= 1)
	isReadingMode := duePages[index].Progress.IntervalDays == 0
	if isReadingMode {
		text += "📖 Прочитай слова и оцени насколько хорошо их помнишь:"
	} else {
		text += "💡 Скопируй эту страницу и отправь в бота Poe для генерации задания.\n\n"
		text += "После прохождения задания отметь результат:"
	}

	// Передаём индекс страницы в кнопки оценки
	callbackData1 := fmt.Sprintf("grade_80_100_%d", index)
	callbackData2 := fmt.Sprintf("grade_60_80_%d", index)
	callbackData3 := fmt.Sprintf("grade_40_60_%d", index)
	callbackData4 := fmt.Sprintf("grade_0_40_%d", index)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Easy (>80%)", callbackData1),
			tgbotapi.NewInlineKeyboardButtonData("🟢 Normal (>60%)", callbackData2),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🟡 Hard (>40%)", callbackData3),
			tgbotapi.NewInlineKeyboardButtonData("🔴 Forgot (<40%)", callbackData4),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("↩️ Пропустить", "skip_page"),
		),
	)

	h.sendMessageWithKeyboard(callback.Message.Chat.ID, text, keyboard)
}

func (h *TelegramHandler) handleGradeReview(ctx context.Context, callback *tgbotapi.CallbackQuery, grade int) {
	// Extract page index from callback data
	data := callback.Data
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID

	var indexStr string
	if strings.HasPrefix(data, "grade_80_100_") {
		indexStr = strings.TrimPrefix(data, "grade_80_100_")
	} else if strings.HasPrefix(data, "grade_60_80_") {
		indexStr = strings.TrimPrefix(data, "grade_60_80_")
	} else if strings.HasPrefix(data, "grade_40_60_") {
		indexStr = strings.TrimPrefix(data, "grade_40_60_")
	} else if strings.HasPrefix(data, "grade_0_40_") {
		indexStr = strings.TrimPrefix(data, "grade_0_40_")
	} else if strings.HasPrefix(data, "success_") || strings.HasPrefix(data, "failure_") {
		// Legacy support - старые callback могут содержать обрезанный pageID
		// Пытаемся восстановить по префиксу (для обратной совместимости)
		pageIDPrefix := ""
		if strings.HasPrefix(data, "success_") {
			pageIDPrefix = strings.TrimPrefix(data, "success_")
			grade = 90
		} else {
			pageIDPrefix = strings.TrimPrefix(data, "failure_")
			grade = 30
		}

		// Восстанавливаем pageID из списка страниц по префиксу
		duePages, err := h.service.GetDuePagesToday(ctx, userID)
		if err != nil {
			if h.handleAuthError(err, userID, chatID) {
				return
			}
			zap.S().Error("get due pages today for legacy callback", zap.Error(err), zap.Int64("telegram_id", userID))
			h.sendMessage(chatID, "Не удалось найти страницу. Попробуй заново через /today")
			return
		}

		var pageID string
		found := false
		for _, pwp := range duePages {
			if strings.HasPrefix(pwp.Page.PageID, pageIDPrefix) {
				pageID = pwp.Page.PageID
				found = true
				break
			}
		}

		if !found {
			zap.S().Warn("page ID not found for legacy callback prefix", zap.String("prefix", pageIDPrefix), zap.Int64("telegram_id", userID))
			h.sendMessage(chatID, "Не удалось найти страницу. Попробуй заново через /today")
			return
		}

		h.updateReviewProgress(ctx, userID, chatID, pageID, grade)
		return
	} else {
		zap.S().Warn("unknown grade callback format", zap.String("data", data))
		return
	}

	// Получаем список страниц для повторения и находим нужную по индексу
	duePages, err := h.service.GetDuePagesToday(ctx, userID)
	if err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("get due pages today", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось получить список страниц. Попробуй заново через /today")
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 || index >= len(duePages) {
		zap.S().Error("invalid page index", zap.String("index", indexStr), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Неверный выбор. Попробуй заново через /today")
		return
	}

	pageID := duePages[index].Page.PageID
	h.updateReviewProgress(ctx, userID, chatID, pageID, grade)
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

func (h *TelegramHandler) handleSkipPage(ctx context.Context, callback *tgbotapi.CallbackQuery) {

	userID := callback.From.ID
	pageID := strings.TrimPrefix(callback.Data, "skip_page_")

	if err := h.service.SkipPage(ctx, userID, pageID); err != nil {
		zap.S().Error("skip page", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("page_id", pageID))
		h.sendMessage(callback.Message.Chat.ID, "Не удалось пропустить страницу. Попробуй позже.")
		return
	}

	h.sendMessage(callback.Message.Chat.ID, "Хорошо, пропустим её на сегодня")
}

func (h *TelegramHandler) handleSkipAll(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	h.sendMessage(callback.Message.Chat.ID, "Хорошо, пропускаем на сегодня. Увидимся завтра! 👋")
}

func (h *TelegramHandler) handleStartTodayYes(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	chatID := callback.Message.Chat.ID

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

	h.sendMessage(chatID, "Подготавливаю материалы...")

	if err := h.service.PrepareMaterials(ctx, userID); err != nil {
		if h.handleAuthError(err, userID, chatID) {
			return
		}
		zap.S().Error("prepare materials", zap.Error(err), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "Не удалось подготовить материалы. Попробуй позже.")
		return
	}

	h.sendMessage(chatID, "✅ Материалы успешно подготовлены! Используй /today для начала занятий.")
}

func (h *TelegramHandler) handleStartTodayNo(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	h.sendMessage(callback.Message.Chat.ID, "Хорошо, используй /today когда будешь готов начать занятия.")
}

func (h *TelegramHandler) handleMaxPagesSelection(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	maxPagesStr := strings.TrimPrefix(callback.Data, "max_pages_")
	chatID := callback.Message.Chat.ID

	maxPagesInt, err := strconv.Atoi(maxPagesStr)
	if err != nil || maxPagesInt < 2 || maxPagesInt > 4 {
		zap.S().Error("invalid max pages value", zap.String("max_pages", maxPagesStr), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, "❌ Некорректное значение. Попробуй ещё раз.")
		return
	}

	maxPages := uint(maxPagesInt)
	if err := h.service.UpdateMaxPagesPerDay(ctx, userID, maxPages); err != nil {
		zap.S().Error("update max pages per day", zap.Error(err), zap.Int64("telegram_id", userID), zap.Uint("max_pages", maxPages))
		h.sendMessage(chatID, "Ошибка при обновлении настроек. Попробуй позже.")
		return
	}

	text := fmt.Sprintf("✅ Максимальное количество страниц в день установлено: %d\n\nТеперь выбери свой город для установки таймзоны:", maxPages)
	h.sendMessage(chatID, text)
	// Показываем выбор таймзоны после установки max_pages
	h.showTimezoneSelector(chatID)
}

func (h *TelegramHandler) handleTimezoneSelection(ctx context.Context, callback *tgbotapi.CallbackQuery) {
	userID := callback.From.ID
	timezoneStr := strings.TrimPrefix(callback.Data, "timezone_")
	chatID := callback.Message.Chat.ID

	// Validate timezone by trying to load it
	_, err := time.LoadLocation(timezoneStr)
	if err != nil {
		zap.S().Error("invalid timezone", zap.Error(err), zap.String("timezone", timezoneStr), zap.Int64("telegram_id", userID))
		h.sendMessage(chatID, fmt.Sprintf("❌ Некорректная таймзона: %s", timezoneStr))
		return
	}

	if err := h.service.UpdateUserTimezone(ctx, userID, timezoneStr); err != nil {
		zap.S().Error("update user timezone", zap.Error(err), zap.Int64("telegram_id", userID), zap.String("timezone", timezoneStr))
		h.sendMessage(chatID, "Ошибка при обновлении таймзоны. Попробуй позже.")
		return
	}

	// Получаем информацию о пользователе, чтобы понять, был ли это этап регистрации
	user, err := h.service.GetUser(ctx, userID)
	isNewUser := false
	if err == nil && user != nil {
		// Проверяем, есть ли у пользователя настройки OneNote
		// Если нет, значит это этап регистрации
		if user.NotebookID == nil || *user.NotebookID == "" {
			isNewUser = true
		}
	}

	if isNewUser {
		h.sendMessage(chatID, fmt.Sprintf("✅ Таймзона установлена: %s\n\nТеперь подключи OneNote с помощью /connect_onenote", timezoneStr))
	} else {
		h.sendMessage(chatID, fmt.Sprintf("✅ Таймзона установлена: %s\n\nНовые материалы будут добавляться автоматически в 00:00 каждый день по твоему местному времени.", timezoneStr))
	}
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
		h.sendMessage(chatID, "Использование: /set_max_pages <b>число</b>\n\nНапример: /set_max_pages 3\n\nРекомендуемые значения: 2, 3 или 4")
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

// showMaxPagesSelector показывает кнопки для выбора максимального количества страниц в день
func (h *TelegramHandler) showMaxPagesSelector(chatID int64) {
	text := "📊 Выбери максимальное количество страниц в день:\n\n2 страницы в день → добавляется 1 страница\n3 страницы в день → добавляется 1 (60%) или 2 (40%)\n4 страницы в день → добавляется 2 страницы"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("2", "max_pages_2"),
			tgbotapi.NewInlineKeyboardButtonData("3", "max_pages_3"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("4", "max_pages_4"),
		),
	)

	h.sendMessageWithKeyboard(chatID, text, keyboard)
}

// showTimezoneSelector показывает кнопки с популярными городами для выбора таймзоны
func (h *TelegramHandler) showTimezoneSelector(chatID int64) {
	text := "🌍 Выбери свой город для установки таймзоны:"

	// Популярные города с их таймзонами
	cities := []struct {
		name     string
		timezone string
		offset   int // Смещение относительно UTC в часах
	}{
		{"Москва", "Europe/Moscow", 3},
		{"Санкт-Петербург", "Europe/Moscow", 3},
		{"Киев", "Europe/Kyiv", 2},
		{"Минск", "Europe/Minsk", 3},
		{"Лондон", "Europe/London", 0},
		{"Париж", "Europe/Paris", 1},
		{"Берлин", "Europe/Berlin", 1},
		{"Рим", "Europe/Rome", 1},
		{"Нью-Йорк", "America/New_York", -5},
		{"Лос-Анджелес", "America/Los_Angeles", -8},
		{"Чикаго", "America/Chicago", -6},
		{"Торонто", "America/Toronto", -5},
		{"Токио", "Asia/Tokyo", 9},
		{"Пекин", "Asia/Shanghai", 8},
		{"Дубай", "Asia/Dubai", 4},
		{"Тегеран", "Asia/Tehran", 3},
		{"Дели", "Asia/Kolkata", 5},
		{"Сидней", "Australia/Sydney", 10},
		{"Сан-Паулу", "America/Sao_Paulo", -3},
		{"Буэнос-Айрес", "America/Argentina/Buenos_Aires", -3},
		{"Каир", "Africa/Cairo", 2},
	}

	var buttons [][]tgbotapi.InlineKeyboardButton

	// Группируем города по 2 в ряд
	for i := 0; i < len(cities); i += 2 {
		var row []tgbotapi.InlineKeyboardButton

		// Первая кнопка в ряду
		offsetStr := formatTimezoneOffset(cities[i].offset)
		buttonText := fmt.Sprintf("%s %s", cities[i].name, offsetStr)
		callbackData := fmt.Sprintf("timezone_%s", cities[i].timezone)
		row = append(row, tgbotapi.NewInlineKeyboardButtonData(buttonText, callbackData))

		// Вторая кнопка в ряду (если есть)
		if i+1 < len(cities) {
			offsetStr2 := formatTimezoneOffset(cities[i+1].offset)
			buttonText2 := fmt.Sprintf("%s %s", cities[i+1].name, offsetStr2)
			callbackData2 := fmt.Sprintf("timezone_%s", cities[i+1].timezone)
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(buttonText2, callbackData2))
		}

		buttons = append(buttons, row)
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(buttons...)
	h.sendMessageWithKeyboard(chatID, text, keyboard)
}

// formatTimezoneOffset форматирует смещение таймзоны в строку типа "UTC+3" или "UTC-5"
func formatTimezoneOffset(offset int) string {
	if offset == 0 {
		return "UTC"
	}
	if offset > 0 {
		return fmt.Sprintf("UTC+%d", offset)
	}
	return fmt.Sprintf("UTC%d", offset) // Для отрицательных значений fmt.Sprintf автоматически добавит минус
}

func (h *TelegramHandler) handleSetTimezone(ctx context.Context, update tgbotapi.Update) {
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

	// Показываем кнопки для выбора таймзоны
	h.showTimezoneSelector(chatID)
}

func (h *TelegramHandler) startDailyCron() {
	// Run immediately on startup
	// ctx := context.Background()
	// if err := h.service.RunDailyCron(ctx); err != nil {
	// 	zap.S().Error("run daily cron on startup", zap.Error(err))
	// }

	// Run every hour to check if it's midnight in any user's timezone
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		ctx := context.Background()
		if err := h.service.RunDailyCron(ctx); err != nil {
			zap.S().Error("run daily cron", zap.Error(err))
		}
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
	ticker := time.NewTicker(30 * time.Minute)
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
		reminder, err := time.Parse("15:04", user.ReminderTime)
		if err != nil {
			zap.S().Error("parse reminder time", zap.Error(err), zap.Int64("telegram_id", user.TelegramID), zap.String("reminder_time", user.ReminderTime))
			continue
		}

		// Get current time in user's timezone
		timezone := "UTC"
		if user.Timezone != nil && *user.Timezone != "" {
			timezone = *user.Timezone
		}

		nowUTC := utils.NowUTC()
		now, err := utils.ToUserTimezone(nowUTC, timezone)
		if err != nil {
			zap.S().Warn("failed to convert to user timezone", zap.Error(err), zap.Int64("telegram_id", user.TelegramID), zap.String("timezone", timezone))
			continue
		}

		if reminder.Hour() != now.Hour() || reminder.Minute() != now.Minute() {
			continue
		}

		duePages, err := h.service.GetDuePagesToday(ctx, user.TelegramID)
		if err != nil {
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
