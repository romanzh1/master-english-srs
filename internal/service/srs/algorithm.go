package srs

import (
	"math/rand"
	"slices"
	"time"

	"github.com/romanzh1/master-english-srs/pkg/utils"
	"go.uber.org/zap"
)

type Grade string

const (
	forgot Grade = "forgot"
	easy   Grade = "easy"
	normal Grade = "normal"
	hard   Grade = "hard"
)

var defaultIntervals = []int{1, 3, 7, 14, 30, 90, 180}

func CalculateNextReviewDate(currentIntervalDays int, success Grade) (time.Time, int) {
	interval := slices.Index(defaultIntervals, currentIntervalDays)

	// Если интервал не найден, используем первый интервал как fallback
	if interval == -1 {
		zap.L().Error("Interval not found, using default", zap.Int("requested_days", currentIntervalDays))
		return calculateInterval(defaultIntervals[0])
	}

	switch success {
	case forgot:
		return calculateInterval(defaultIntervals[0])
	case easy:
		if interval == len(defaultIntervals)-1 {
			return calculateInterval(defaultIntervals[interval])
		}

		return calculateInterval(defaultIntervals[interval+1])
	case normal:
		return calculateInterval(defaultIntervals[interval])
	case hard:
		if interval == 0 {
			return calculateInterval(defaultIntervals[interval])
		}

		return calculateInterval(defaultIntervals[interval-1])
	}

	return calculateInterval(defaultIntervals[interval] + 1)
}

func calculateInterval(interval int) (time.Time, int) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		moscow = time.UTC
	}

	now := time.Now().In(moscow)
	t := now.AddDate(0, 0, interval)

	return utils.StartOfDay(t), interval
}

func GetInitialReviewDate() (time.Time, int) {
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		moscow = time.UTC
	}

	now := time.Now().In(moscow)
	tomorrow := now.AddDate(0, 0, 1)

	return utils.StartOfDay(tomorrow), 1
}

// CalculatePagesToAdd determines how many pages to add to learning based on max pages per day
// maxPagesPerDay = 2 → returns 1
// maxPagesPerDay = 3 → randomly returns 1 (60%) or 2 (40%)
// maxPagesPerDay = 4 → returns 2
func CalculatePagesToAdd(maxPagesPerDay uint) int {
	switch maxPagesPerDay {
	case 0:
		return 1
	case 1:
		return 1
	case 2:
		return 1
	case 3:
		// 60% chance for 1 page, 40% chance for 2 pages
		if rand.Float32() < 0.6 {
			return 1
		}
		return 2
	case 4:
		return 2
	default:
		// For values > 4, return half (rounded down) or apply similar logic
		return int(maxPagesPerDay / 2)
	}
}

// ConvertGradeToStatus converts percentage grade to Grade status
// >80% → easy
// >60% → normal
// >=40% → hard
// <40% → forgot
func ConvertGradeToStatus(grade int) Grade {
	if grade > 80 {
		return easy
	} else if grade > 60 {
		return normal
	} else if grade >= 40 {
		return hard
	}
	return forgot
}
