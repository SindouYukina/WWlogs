# Bundled translations

`manifest.json` is the only supported-language catalog. English (`en`) is the UI baseline and fallback. Each approved locale contains the same JSON namespaces, the ones `manifest.json` lists: `common`, `dashboard`, `history`, `overlay`, `settings`, and `startup`. `startup` holds the few messages the desktop host shows before any window exists, when a launch cannot start (for example because the running WWlogs does not respond).

To contribute a language:

1. Add its JSON folder using the English semantic keys. Preserve every `{{parameter}}` name.
2. Add the locale and its native label to the manifest. `masterLocale` selects an existing SQLite localization; it does not select a game server or protocol.
3. Run `scripts/frontend.ps1 -Action Typecheck` from the repository root. It checks JSON structure, key parity, interpolation parameters, manifest coverage, and frontend types.
4. Submit the JSON and manifest changes for maintainer review.
5. After approval and merge, rebuild the application. Only a new official build can add a supported language.

Vite bundles every approved JSON resource into the frontend archive and includes a normalized manifest and a copy of each language's `startup` namespace (`locales/<locale>/startup.json`) for the Go host. Runtime language packs, directory scanning, translation downloads, and external translation APIs are not supported. Missing UI keys fall back through i18next to English; Master names separately fall back to their English SQLite row, then a localized neutral unknown label.

UI localization is display-only. Machine statuses, IDs, ownership, damage, and DPS retain their original meanings. Effect glossary links retain their evidence level; a translated glossary term is not proof of a confirmed effect name or polarity.
