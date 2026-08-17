package weather

// SelectDate keeps the requested calendar day from a forecast window.
//
// Open-Meteo / NWS / MSC only return "today" (at the location) onward.
// A TZ or UTC-today mismatch can make the caller ask for yesterday, which
// used to empty the list and surface as "Météo indisponible". If the
// requested date is on or before the first forecast day, keep that current
// day instead of dropping it.
func SelectDate(days []ForecastDay, date string) []ForecastDay {
	if date == "" || len(days) == 0 {
		return days
	}
	for _, d := range days {
		if d.Date == date {
			return []ForecastDay{d}
		}
	}
	if date <= days[0].Date {
		return []ForecastDay{days[0]}
	}
	return nil
}
