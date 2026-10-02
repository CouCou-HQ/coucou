package settings

import "github.com/disgoorg/disgo/discord"

// localeZones holds only the locales whose speakers mostly share one zone. Those spread across
// several — en-US, es-419, ru, id — are left out and resolve to UTC, which is visibly unset
// rather than confidently wrong.
var localeZones = map[discord.Locale]string{
	discord.LocaleBulgarian:    "Europe/Sofia",
	discord.LocaleChineseCN:    "Asia/Shanghai",
	discord.LocaleChineseTW:    "Asia/Taipei",
	discord.LocaleCroatian:     "Europe/Zagreb",
	discord.LocaleCzech:        "Europe/Prague",
	discord.LocaleDanish:       "Europe/Copenhagen",
	discord.LocaleDutch:        "Europe/Amsterdam",
	discord.LocaleEnglishGB:    "Europe/London",
	discord.LocaleFinnish:      "Europe/Helsinki",
	discord.LocaleFrench:       "Europe/Paris",
	discord.LocaleGerman:       "Europe/Berlin",
	discord.LocaleGreek:        "Europe/Athens",
	discord.LocaleHindi:        "Asia/Kolkata",
	discord.LocaleHungarian:    "Europe/Budapest",
	discord.LocaleItalian:      "Europe/Rome",
	discord.LocaleJapanese:     "Asia/Tokyo",
	discord.LocaleKorean:       "Asia/Seoul",
	discord.LocaleLithuanian:   "Europe/Vilnius",
	discord.LocaleNorwegian:    "Europe/Oslo",
	discord.LocalePolish:       "Europe/Warsaw",
	discord.LocalePortugueseBR: "America/Sao_Paulo",
	discord.LocaleRomanian:     "Europe/Bucharest",
	discord.LocaleSpanishES:    "Europe/Madrid",
	discord.LocaleSwedish:      "Europe/Stockholm",
	discord.LocaleThai:         "Asia/Bangkok",
	discord.LocaleTurkish:      "Europe/Istanbul",
	discord.LocaleUkrainian:    "Europe/Kyiv",
	discord.LocaleVietnamese:   "Asia/Ho_Chi_Minh",
}
