package utils

import "time"

func StartOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func TruncateToMinutes(t time.Time) time.Time {
	return t.Truncate(time.Minute)
}

func NowUTC() time.Time {
	return time.Now().UTC()
}

func ToUserTimezone(t time.Time, timezone string) (time.Time, error) {
	if timezone == "" {
		return t, nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return t, err
	}
	return t.In(loc), nil
}

// StartOfDayInTimezone returns the start of day in the specified timezone
func StartOfDayInTimezone(t time.Time, timezone string) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return t, err
	}
	tInTz := t.In(loc)
	return time.Date(tInTz.Year(), tInTz.Month(), tInTz.Day(), 0, 0, 0, 0, loc), nil
}

func StartOfTodayUTC() time.Time {
	return StartOfDay(NowUTC())
}

func StartOfTodayInTimezone(timezone string) (time.Time, error) {
	return StartOfDayInTimezone(NowUTC(), timezone)
}

// IsFirstHourOfDayInTimezone checks if it's the first hour of the day (00:00-00:59) in the specified timezone
func IsFirstHourOfDayInTimezone(timezone string) (bool, error) {
	if timezone == "" {
		return false, nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return false, err
	}
	now := time.Now().In(loc)
	return now.Hour() == 0, nil
}
