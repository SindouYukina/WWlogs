# Third-Party Notices

WWlogs is licensed under the **GNU Affero General Public License v3.0 only** (SPDX identifier `AGPL-3.0-only`). The complete license text is in [LICENSE](LICENSE).

That license applies only to WWlogs' own source code. The game material, fonts and third-party software listed below, which this repository contains or WWlogs executables include, remain under the rights and licenses of their respective holders. Nothing in this file grants a right that those holders have not granted.

## 1. WWlogs source code

The source code written for WWlogs, including its Go, TypeScript, CSS, PowerShell and test sources, is licensed under AGPL-3.0-only:

> Copyright (C) 2026 SindouYukina
>
> WWlogs is free software: you can redistribute it and/or modify it under the terms of the GNU Affero General Public License as published by the Free Software Foundation, version 3.
>
> WWlogs is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU Affero General Public License for more details.

This copyright notice covers only the source code written for WWlogs. It does not claim the Wuthering Waves material, the fonts or the third-party software in sections 2 to 4, which belong to their respective holders. Files copied from the third-party projects in section 4 keep their own licenses.

## 2. Wuthering Waves game material

Wuthering Waves (鳴潮) game art and intellectual property belong to KURO GAMES or their respective rights holders. In this repository and in WWlogs executables, this includes:

- the character images, skill icons, echo skill icons and effect icons under `assets/game/`;
- the theme backgrounds and their thumbnails in `frontend/src/design/themes/art/`, prepared from Wuthering Waves character art;
- the game names, text and other values taken from the game client that `data/master_data.db` contains, and the character names in the localization files.

This material is not part of WWlogs' own source code and is not covered by its AGPL-3.0-only license. Its presence in this repository or in an executable does not grant any right to use or redistribute it.

WWlogs is an independently developed, unofficial tool. It does not represent KURO GAMES and does not claim any official partnership, sponsorship or endorsement. The Wuthering Waves and KURO GAMES names and marks belong to their respective owners and are used here only to identify the game.

Official website: <https://wutheringwaves.kurogames.com/>

## 3. Fonts

WWlogs executables embed the fonts in `frontend/src/design/fonts/`. Their complete license files are in `frontend/src/design/fonts/LICENSES/` and are shown, untranslated, in Settings > Licenses.

| Font | Version | License | Copyright notice |
| --- | --- | --- | --- |
| Inter (Inter Variable) | 4.1 | SIL Open Font License 1.1 (`OFL-1.1`) | Copyright (c) 2016 The Inter Project Authors (https://github.com/rsms/inter) |
| Noto Sans CJK (Noto Sans TC, SC, JP and KR) | 2.004 | SIL Open Font License 1.1 (`OFL-1.1`) | Copyright 2014-2021 Adobe (http://www.adobe.com/), with Reserved Font Name 'Source' |

## 4. Third-party software

Each component below remains under its own license. Its complete license text is part of its source at the listed version: the `LICENSE` file of the module or package, and any further license and notice files it carries, such as the Go `PATENTS` grant and the `LICENSE-3RD-PARTY.md` notices of the modernc.org modules. None of the components ships a `NOTICE` file. Unmodified copies of these files for every component in sections 4.1 and 4.2 are in `frontend/src/lib/library-licenses/`, listed by its `manifest.json`, and WWlogs executables show them offline and untranslated in Settings > Licenses.

### 4.1 Linked into WWlogs.exe

The Development and Public executables link the same Go modules. The list is the build information of `WWlogs.exe` (`go version -m`), which a test compares with the modules the desktop executable imports in both profiles; the versions are pinned in `go.mod` and `go.sum`.

| Component | Version | License | Copyright notice |
| --- | --- | --- | --- |
| Go standard library and runtime | the Go toolchain of the build, which `go version -m` reports (at least the `go` version in `go.mod`) | BSD-3-Clause, with the Go additional patent grant (`PATENTS`) | Copyright 2009 The Go Authors. |
| `github.com/dustin/go-humanize` | v1.1.0 | MIT | Copyright (c) 2005-2008 Dustin Sallings <dustin@spy.net> |
| `github.com/go-ole/go-ole` | v1.3.0 | MIT | Copyright © 2013-2017 Yasuhiro Matsumoto, <mattn.jp@gmail.com> |
| `github.com/google/gopacket` | v1.1.19 | BSD-3-Clause | Copyright (c) 2012 Google, Inc. All rights reserved. Copyright (c) 2009-2011 Andreas Krennmair. All rights reserved. |
| `github.com/mattn/go-isatty` | v0.0.24 | MIT | Copyright (c) Yasuhiro MATSUMOTO <mattn.jp@gmail.com> |
| `github.com/ncruces/go-sqlite3` | v0.37.0 | MIT | Copyright (c) 2023 Nuno Cruces |
| `github.com/ncruces/go-sqlite3-wasm/v6` | v6.5.35401 | MIT-0; the SQLite it is built from is in the public domain | Copyright (c) 2026 Nuno Cruces |
| `github.com/ncruces/go-strftime` | v1.1.0 | MIT | Copyright (c) 2022 Nuno Cruces |
| `github.com/ncruces/julianday` | v1.0.0 | MIT | Copyright (c) 2022 Nuno Cruces |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | Copyright (c) 2012 The Go Authors. All rights reserved. |
| `github.com/wailsapp/wails/v3` | v3.1.0 | MIT | Copyright (c) 2018-Present Lea Anthony |
| `golang.org/x/crypto` | v0.58.0 | BSD-3-Clause, with the Go additional patent grant (`PATENTS`) | Copyright 2009 The Go Authors. |
| `golang.org/x/sys` | v0.49.0 | BSD-3-Clause, with the Go additional patent grant (`PATENTS`) | Copyright 2009 The Go Authors. |
| `modernc.org/libc` | v1.79.0 | BSD-3-Clause; further notices in its `LICENSE-3RD-PARTY.md` | Copyright (c) 2017 The Libc Authors. All rights reserved. |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause | Copyright (c) 2014 The mathutil Authors. All rights reserved. |
| `modernc.org/memory` | v1.12.1 | BSD-3-Clause; further notices in its `LICENSE-GO` and `LICENSE-MMAP-GO` | Copyright (c) 2017 The Memory Authors. All rights reserved. |
| `modernc.org/sqlite` | v1.61.1 | BSD-3-Clause; SQLite itself is in the public domain (`LICENSE-SQLITE`); further notices in its `LICENSE-3RD-PARTY.md` | Copyright (c) 2017 The Sqlite Authors. All rights reserved. |

### 4.2 Bundled into the WWlogs.exe frontend

The Development and Public frontends bundle the same npm packages. The list is the set of packages whose modules or generated code the production bundle contains, and the frontend build fails when its bundle and the license manifest disagree; the versions are pinned in `frontend/package-lock.json`.

| Component | Version | License | Copyright notice |
| --- | --- | --- | --- |
| `react` | 19.3.1 | MIT | Copyright (c) Meta Platforms, Inc. and affiliates. |
| `react-dom` | 19.3.1 | MIT | Copyright (c) Meta Platforms, Inc. and affiliates. |
| `scheduler` | 0.28.0 | MIT | Copyright (c) Meta Platforms, Inc. and affiliates. |
| `use-sync-external-store` | 1.7.0 | MIT | Copyright (c) Meta Platforms, Inc. and affiliates. |
| `i18next` | 26.7.2 | MIT | Copyright (c) 2011-present i18next |
| `react-i18next` | 17.1.13 | MIT | Copyright (c) 2015-present i18next |
| `@wailsio/runtime` | 3.1.0 | MIT (declared by the package, which carries no license file of its own; the Wails repository's license applies) | Copyright (c) 2018-Present Lea Anthony |
| `tailwindcss` | 4.3.4 | MIT (the generated stylesheet, including its base styles) | Copyright (c) Tailwind Labs, Inc. |
| `vite` | 8.3.6 | MIT (the module preload helper it adds to the bundle) | Copyright (c) 2019-present, VoidZero Inc. and Vite contributors |
| `rolldown` | 1.2.16 | MIT (the module runtime helpers it adds to the bundle; its `THIRD-PARTY-LICENSE` carries the Rollup and esbuild notices) | Copyright (c) 2024-present VoidZero Inc. & Contributors |

### 4.3 Copied into this repository

These source files are third-party code under their own license. Each file names its source in its header comment.

| Component | Files | License | Copyright notice |
| --- | --- | --- | --- |
| shadcn/ui components (registry style new-york-v4, retrieved 2026-11-05) | `frontend/src/components/ui/badge.tsx`, `button.tsx`, `card.tsx` | MIT | Copyright (c) 2023 shadcn |

### 4.4 Not included in WWlogs executables

The other build tools, such as Node.js, npm, TypeScript, `@vitejs/plugin-react`, `@tailwindcss/vite` and `github.com/tc-hib/winres`, run only while WWlogs is built.

Npcap and the Microsoft Edge WebView2 Runtime are separate products that users install themselves. WWlogs does not include them.
