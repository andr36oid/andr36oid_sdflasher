package i18n

// awaitingTranslation are the en.json keys that are new since the last translation
// round. Until the translator has done them the other languages show them in English
// (Text falls back), and TestCatalogsComplete lets them be missing there. Remove a key
// here once it is translated into every language.
var awaitingTranslation = map[string]bool{}
