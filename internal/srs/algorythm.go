package srs

import (
	"time"
)

var defaultIntervals = []int{1, 3, 7, 14, 30}

func CalculateNextReviewDate(currentIntervalDays int, success bool) (time.Time, int) {
	if !success {
		return time.Now().AddDate(0, 0, 1), 1
	}

	for i, interval := range defaultIntervals {
		if interval == currentIntervalDays && i < len(defaultIntervals)-1 {
			nextInterval := defaultIntervals[i+1]
			return time.Now().AddDate(0, 0, nextInterval), nextInterval
		}
	}

	return time.Now().AddDate(0, 0, 30), 30
}

func GetInitialReviewDate() (time.Time, int) {
	return time.Now().AddDate(0, 0, 1), 1
}

func ShouldReviewToday(nextReviewDate time.Time) bool {
	now := time.Now()
	return nextReviewDate.Before(now) || nextReviewDate.Equal(now.Truncate(24*time.Hour))
}
