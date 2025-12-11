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

func CalculateNextReviewDate(currentIntervalDays int, success Grade, timezone string) (time.Time, int) {
	interval := slices.Index(defaultIntervals, currentIntervalDays)

	// Если интервал не найден, используем первый интервал как fallback
	if interval == -1 {
		zap.L().Error("Interval not found, using default", zap.Int("requested_days", currentIntervalDays))
		return calculateInterval(defaultIntervals[0], timezone)
	}

	switch success {
	case forgot:
		return calculateInterval(defaultIntervals[0], timezone)
	case easy, normal:
		if interval == len(defaultIntervals)-1 {
			return calculateInterval(defaultIntervals[interval], timezone)
		}

		return calculateInterval(defaultIntervals[interval+1], timezone)
	case hard:
		if interval == 0 {
			return calculateInterval(defaultIntervals[interval], timezone)
		}

		return calculateInterval(defaultIntervals[interval-1], timezone)
	}

	return calculateInterval(defaultIntervals[interval]+1, timezone)
}

func calculateInterval(interval int, timezone string) (time.Time, int) {
	// Convert to user's timezone to get "today" in their timezone
	var startOfDayInTz time.Time
	var err error
	if timezone != "" {
		startOfDayInTz, err = utils.StartOfTodayInTimezone(timezone)
		if err != nil {
			zap.L().Warn("Failed to get start of day in timezone, using UTC", zap.String("timezone", timezone), zap.Error(err))
			startOfDayInTz = utils.StartOfTodayUTC()
		}
	} else {
		startOfDayInTz = utils.StartOfTodayUTC()
	}

	// Add interval days in user's timezone
	t := startOfDayInTz.AddDate(0, 0, interval)

	// Convert back to UTC for database storage
	return t.UTC(), interval
}

// GetInitialReviewDate returns today's date with interval 0 (reading mode)
func GetInitialReviewDate(timezone string) (time.Time, int) {
	var startOfDayInTz time.Time
	var err error
	if timezone != "" {
		startOfDayInTz, err = utils.StartOfTodayInTimezone(timezone)
		if err != nil {
			zap.L().Warn("Failed to get start of day in timezone, using UTC", zap.String("timezone", timezone), zap.Error(err))
			startOfDayInTz = utils.StartOfTodayUTC()
		}
	} else {
		startOfDayInTz = utils.StartOfTodayUTC()
	}

	// Convert back to UTC for database storage
	return startOfDayInTz.UTC(), 0
}

// GetNextDayReviewDate returns tomorrow's date with interval 1 (transition to AI mode)
func GetNextDayReviewDate(timezone string) (time.Time, int) {
	var startOfDayInTz time.Time
	var err error
	if timezone != "" {
		startOfDayInTz, err = utils.StartOfTodayInTimezone(timezone)
		if err != nil {
			zap.L().Warn("Failed to get start of day in timezone, using UTC", zap.String("timezone", timezone), zap.Error(err))
			startOfDayInTz = utils.StartOfTodayUTC()
		}
	} else {
		startOfDayInTz = utils.StartOfTodayUTC()
	}

	tomorrow := startOfDayInTz.AddDate(0, 0, 1)

	// Convert back to UTC for database storage
	return tomorrow.UTC(), 1
}

// GetNextDayReadingMode returns tomorrow's date with interval 0 (stay in reading mode)
func GetNextDayReadingMode(timezone string) (time.Time, int) {
	var startOfDayInTz time.Time
	var err error
	if timezone != "" {
		startOfDayInTz, err = utils.StartOfTodayInTimezone(timezone)
		if err != nil {
			zap.L().Warn("Failed to get start of day in timezone, using UTC", zap.String("timezone", timezone), zap.Error(err))
			startOfDayInTz = utils.StartOfTodayUTC()
		}
	} else {
		startOfDayInTz = utils.StartOfTodayUTC()
	}

	tomorrow := startOfDayInTz.AddDate(0, 0, 1)

	// Convert back to UTC for database storage
	return tomorrow.UTC(), 0
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
