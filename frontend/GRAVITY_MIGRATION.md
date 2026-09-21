# Migration Plan: Frontend → Gravity UI

Статус: **выполнено** (фазы 0–4). Источник правил — официальный skill `gravity-ui`
(`.agents/skills/gravity-ui`, установлен через `npx skills add gravity-ui/skills`)
+ `https://gravity-ui.com/llms/uikit/llms.txt`.

## Итог миграции

- `@gravity-ui/uikit@7.49.0` + `@gravity-ui/icons@2.22.0`; Tailwind удалён полностью
  (deps, плагин vite, утилити-классы) — layout на `--g-*` токенах (`src/styles/app.css`).
- Все страницы переведены: Table/Select/TextInput/TextArea/PasswordInput/Checkbox/Slider/
  Tabs/Modal/Drawer/Toaster/Label/Progress/Card/ClipboardButton/Text.
- `window.prompt` и самодельные confirm-блоки заменены на `Modal` (+ причина в audit).
- Стилизация: `ThemeProvider theme="system"`, SSR-класс `g-root g-root_theme_light` на
  `<body>` против FOUC; стили подключены через `?url`-линки в head.
- SSR: обязательно `ssr.noExternal: [/@gravity-ui\//]` в vite.config — иначе Node падает
  на css-импортах внутри ESM-сборки uikit.

## Нюансы API, вскрытые typecheck'ом (правило скилла «typechecker — источник истины»)

| Место | Документировано | На деле (v7.49) |
| --- | --- | --- |
| `Select` опции | `options=[…]` (не `items`) | подтверждено; `value: string[]`, `onUpdate(v: string[])` — всегда массив |
| `Checkbox` | нет prop `label` | текст через children |
| `TextArea` | нет prop `label` | подпись — `Text` сверху |
| `toaster.add()` | — | обязателен уникальный `name` |
| `Select` | нет prop `style` | ширина через `className` |
| `Slider` | есть в uikit (план ошибочно предполагал отсутствие) | `min/max/step/value/onUpdate(value: number \| [number, number])` |

## Верификация

- `tsc --noEmit` + `vite build` — зелёные.
- SSR smoke: все маршруты 200; `/h/demo-hack-2026` рендерит команды на сервере
  (`Turbo Forge 43`, …), `g-root_theme_light` в HTML, 3 stylesheet-линка (fonts/styles/app).
- Prod-сборка проходит с `ssr.noExternal`.

## Оставшиеся идеи (не блокеры)

- `@gravity-ui/date-components` (DatePicker) вместо `datetime-local`.
- Кастомный брендинг: полный набор `--g-color-base-brand-*` в обеих темах.
- Переключатель темы в UI (сейчас `theme="system"` по предпочитаемой ОС).


## Цели

- Единая дизайн-система: контролы, таблицы, модалки, тосты, тема light/dark из коробки.
- Убрать самописные стили (Tailwind-классы в разметке) там, где есть готовый компонент.
- Заменить `window.prompt` на нормальные модалки, алерты — на Toaster.

## Не-цели (пока)

- `@gravity-ui/table` (headless грид) — 50 команд помещаются в uikit `Table`.
- `@gravity-ui/charts` — Sparkline/LineChart остаются кастомными SVG (2 маленьких компонента).
- `@gravity-ui/navigation` — текущий хедер проще оставить на Flex/Box.

## Пакеты

| Пакет | Зачем | Примечание |
| --- | --- | --- |
| `@gravity-ui/uikit` @ 7.x | база: контролы, Table, Tabs, Modal, Drawer, Toaster, Label, Flex/Box, ThemeProvider | peer: React 19 ✓ (текущий стек). **Обязателен в любом Gravity-проекте** |
| `@gravity-ui/icons` | иконки (`<Icon data={X}/>` — у `Icon` НЕТ prop `name`) | замена текстовых «↗», «✕», «▲/▼» |
| `@gravity-ui/date-components` (опц.) | DatePicker для дат создания хакатона | фаза 4, по желанию |

## Правила (hard rules из скилла — нарушать нельзя)

1. `Button` использует `view`/`size`, не `variant`/`color`.
2. `Icon` принимает компонент через `data`, не имя строки.
3. `theme` ∈ `light | dark | light-hc | dark-hc`, `"default"` не существует.
4. Брендинг: переопределять ВЕСЬ набор `--g-color-base-brand-*` токенов в обеих темах,
   не один цвет.
5. Не стилизовать внутренности компонентов селекторами `[class*=…]` — лестница:
   глобальный токен → CSS API компонента → помеченный хак.
6. Типографика: `<Text variant="…"/>`, вариантов `header-1/2` и `subheader-3..6` НЕТ —
   сверяться с `node_modules/@gravity-ui/uikit/dist(docs)/INDEX.md` установленной версии.
7. Перед каждой фазой: Step 0–1 скилла — маршрутизация пакета + чтение
   `node_modules/@gravity-ui/<pkg>/build/docs/` (или `…/llms/<pkg>/7/llms.txt`),
   после — `tsc --noEmit` и `vite build`.

## Фазы

### Фаза 0 — сетап (0.5 дня)

1. `npm i @gravity-ui/uikit @gravity-ui/icons`
2. В entry (`src/routes/__root.tsx` или общий layout-модуль):
   ```tsx
   import '@gravity-ui/uikit/styles/fonts.css'
   import '@gravity-ui/uikit/styles/styles.css'
   ```
3. Обернуть приложение:
   ```tsx
   import {ThemeProvider} from '@gravity-ui/uikit'
   <ThemeProvider theme="light" dir="ltr">…</ThemeProvider>
   ```
4. SSR: прочитать `build/docs/guides/server-side-rendering.md` установленной версии;
   TanStack Start рендерит в поток — убедиться, что стили попадают в критический CSS
   (vite SSR собирает css-imports; проверить `dist/server`).
5. Тема: сейчас dark-режим сделан классами Tailwind (`dark:`) на `prefers-color-scheme`.
   Заменить на `<html data-theme>` + `ThemeProvider theme={resolvedTheme}`; синхронизация
   с `matchMedia('(prefers-color-scheme: dark)')`. Кастомный `app.css` — по минимуму
   (только страница-специфика).
6. Tailwind пока ОСТАВЛЯЕМ (сосуществование): отключить `@tailwindcss/pite`
   preflight-конфликт, если появится (проверить кнопки/инпуты визуально).

### Фаза 1 — каркас и навигация (0.5 дня)

- `__root.tsx`: хедер на `Flex` + `Button view="flat"` для ссылок, `Text` для лого.
- Тостер: смонтировать `<Toaster/>` в корне; хелпер `toast()` для ошибок мутаций
  (заменяет разрозненные `<span className="text-red-600">{error}</span>`).
- `index.tsx`: карточки хакатонов → `Card` + `Label` для статусов
  (draft/running/judging/finalized), кнопка «+ Create» → `Button view="action"`.

### Фаза 2 — публичные страницы (1 день)

- `h.$slug.tsx` (leaderboard):
  - таблица → uikit `Table` (columns: rank/team/track/score/Δ/completeness/status/trend);
  - поиск → `TextInput` с `hasClear`; селекты треков/статусов → `Select`;
  - статус-бейджи → `Label theme="success|warning|info"`;
  - полнота → `ProgressBar`;
  - BreakdownDrawer → `Drawer` (тот же контент, `Sheet`-стиль);
  - текст → `Text variant="header-1|body-2|caption-1"`.
- `h.$slug.teams.$teamId.tsx`: breakdown-таблица → `Table`, ссылки → `Link`-стиль
  через токены.

### Фаза 3 — формы (1 день)

- `login.tsx`: `TextInput type="email/password"` (+`PasswordInput`), `Button view="action"`,
  ошибки — `Text color="danger"` + Toaster.
- `hackathons.new.tsx`: все инпуты → `TextInput`/`TextArea`/`NumberInput`(проверить
  наличие в установленной версии), даты → `DatePicker` из `@gravity-ui/date-components`
  (или `TextInput type="datetime-local"` как минимум), слаг-хинт → `Text variant="code"`.
- `judge.$slug.tsx`:
  - критерии: слайдера в uikit НЕТ (не выдумывать!) — вариант A: `TextInput type="number"`
    + `ProgressBar`-подсказка; вариант B: нативный `<input type="range">`, стилизованный
    `--g-*` токенами (узкий custom CSS, помечен как осознанный хак);
  - comment → `TextArea`; recuse → `TextArea` + `Button view="outlined-danger"`;
  - прогресс «X/Y scored» → `ProgressBar`.
- `app.hackathons.$id.tsx` (консоль):
  - табы → `Tabs` (items из TABS);
  - все `Table`-таблицы → uikit `Table`;
  - формы Add metric/rubric → `TextInput`/`Select`/`Checkbox`/`NumberInput`;
  - **`window.prompt` в StatusControls → `Modal` + `TextArea` для reason**;
  - Lock/Start/judging → `Button view={action|outlined}` (+ `confirm` через `Modal`);
  - IngestTab: токен + `ClipboardButton` (готовый компонент!) вместо ручного копирования;
  - Finalize: подтверждающий `Modal` вместо самодельного confirm-блока.

### Фаза 4 — расчищение хвостов (0.5–1 день)

- Удалить Tailwind (`@tailwindcss/vite`, `app.css`-tailwind-импорты, `dark:` классы,
  `tailwind-merge`), layout → `Flex/Box/Container/Row/Col`, отступы → `--g-spacing-*`.
- Кастомные цвета (`text-indigo-600` и т.п.) → токены (`--g-color-text-brand`,
  `--g-color-text-danger`…). Если нужен фирменный цвет — переопределить полный
  brand-набор в обеих темах (правило №4).
- Брендинг при необходимости: `--g-color-base-brand-*` на light и dark.
- Проверить a11y (focus-ring от uikit) и бандл-сайз (uikit tree-shakable — импорты
  по именам уже точечные).

## Маппинг «сейчас → Gravity»

| Сейчас | Станет |
| --- | --- |
| `<button className="rounded bg-indigo-600…">` | `<Button view="action">` |
| `<input className="rounded border…">` | `TextInput` / `TextArea` / `Select` |
| `<table>` + Tailwind-классы | uikit `Table` |
| самодельные табы-кнопки | `Tabs` |
| BreakdownDrawer (`aside`) | `Drawer` |
| confirm-блок Finalize, `window.prompt` | `Modal` |
| `<span className="text-red-600">` ошибки | Toaster + `Text color="danger"` |
| статус-бейджи `rounded bg-amber-100…` | `Label` |
| прогресс-полосы (div width%) | `ProgressBar` |
| «✕ / ↗ / ▲ / ▼» текстом | `@gravity-ui/icons` + `<Icon data={…}>` |
| `rounded border…` карточки | `Card` |
| Tailwind `flex/gap/p-*` | `Flex`/`Box` (+ `--g-spacing-*`) |

## Риски и проверки

| Риск | Митигация |
| --- | --- |
| SSR-гидратация темы (ThemeProvider) | гайд SSR из установленной версии; тема через `data-theme` на `<html>`, одинаковая на сервере и клиенте при первом рендере |
| Tailwind preflight vs uikit reset | фаза 0 — визуальный smoke обеих систем; фаза 4 — Tailwind удаляется полностью |
| Отсутствие Slider в uikit | не изобретать API; range стилизован токенами или number-input |
| Версионный дрейф API | перед каждой фазой читать `build/docs/INDEX.md` установленной версии; после — `tsc` + `vite build` (правило скилла Step 2) |
| Bundle size | точечные именованные импорты; отчёт `vite build` до/после |

## Definition of Done

- Ни одного `window.prompt/alert`, ни одного самописного модального div.
- Все контролы и таблицы — из `@gravity-ui/*`; Tailwind удалён.
- `tsc --noEmit` и `vite build` зелёные; light/dark переключаются без FOUC.
- Визуальный проход по всем 7 маршрутам (/ , /login, /hackathons/new, /h/$slug,
  /h/$slug/teams/$teamId, /judge/$slug, /app/hackathons/$id).
