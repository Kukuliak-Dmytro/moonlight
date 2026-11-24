# moonlight

Кросплатформний Go-демон і CLI для автоматичного керування **яскравістю** та **нічним світлом** зовнішнього монітора на **Windows** і **macOS**.

Спроєктовано та перевірено на зв'язці **Gigabyte M27UP (KVM) + Windows PC + MacBook**:

- **Автоматичний адаптер моніторів (`moonlight select`)** — на старті сам сканує підключені монітори, визначає виробника/профіль (`Gigabyte`, `Dell`, `LG`, `ASUS`, `Samsung`, `BenQ`, `Generic DDC/CI`), показує інтерактивний вибір і запам'ятовує його в `config.json`. Змінити монітор на інший можна будь-коли однією командою `moonlight select`.
- **Апаратна яскравість по DDC/CI** — крутить реальну підсвітку монітора по відеокабелю (`DisplayPort` / `HDMI` / `USB-C`), не «сірить» чорний колір.
- **Плавне нічне світло через гаму ОС** (`night_light_mode: "gamma"`) — працює в будь-якому режимі монітора (`Standard`, `sRGB`, `Custom`), без обмежень прошивки. За бажанням можна перемкнути на апаратний DDC/CI RGB (`"ddc"`) або комбінований режим (`"both"`).
- **Нуль зовнішніх Go-залежностей** (`CGO_ENABLED=0`) — легкий ~3 МБ бінарник, кроскомпілюється під Windows (`amd64`) і macOS (`arm64` / `amd64`) з однієї кодової бази.
- **Стійкість до KVM і сну** — дескриптори монітора перевідкриваються на кожному циклі; щойно ви перемикаєте KVM назад, демон одразу підтягує актуальну яскравість і теплоту.
- **Hot-reload конфігу** — редагуйте `config.json` на льоту без перезапуску демона; помилковий конфіг відхиляється, попередній безпечний продовжує працювати.
- **Багаторівневий захист від «чорного екрана»** — нижні ліміти безпеки, автобекап початкових значень, `-dry-run`, тест з авто-відкатом `-confirm` та аварійний скид `moonlight reset`.

---

## Як це працює під капотом

| Функція | Windows (`amd64`) | macOS (`arm64` / `amd64`) |
|---|---|---|
| **Яскравість (DDC/CI)** | Нативний WinAPI (`user32.dll` + `dxva2.dll`, VCP `0x10`) | [`m1ddc`](https://github.com/waydabber/m1ddc) (Apple Silicon) або `ddcctl` (Intel) |
| **Нічне світло (`gamma`)** | Нативний WinAPI (`gdi32.dll` → `SetDeviceGammaRamp`) | Вбудований `CoreGraphics` (`CGSetDisplayTransferByFormula`) |
| **Нічне світло (`ddc`)** | WinAPI `dxva2.dll` (VCP `0x16`/`0x18`/`0x1A` + `0x14`) | `m1ddc` (`red`/`green`/`blue`) |
| **Автозапуск у фоні** | Windows Task Scheduler (приховано через `wscript.exe`) | macOS `launchd` (`LaunchAgents`) |
| **Конфіг і стан** | `%APPDATA%\moonlight\` | `~/Library/Application Support/moonlight/` |

---

## Дефолтна 24-годинна крива

Значення між ключовими точками інтерполюються лінійно щохвилини (включно з переходом через північ):

| Час | Яскравість | Температура | RGB-коефіцієнти |
|---|---:|---:|---|
| `20:00 – 09:30` | `0%` | `3400K` | `[100, 75, 54]` *(стабільне нічне плато)* |
| `10:00` | `70%` | `6500K` | `[100, 100, 100]` *(старт робочого дня)* |
| `12:00` | `60%` | `6200K` | `[100, 98, 97]` |
| `14:00` | `50%` | `5800K` | `[100, 96, 92]` |
| `16:00` | `40%` | `5300K` | `[100, 92, 86]` |
| `17:00` | `30%` | `4800K` | `[100, 88, 80]` *(−10%/год)* |
| `18:00` | `20%` | `4300K` | `[100, 84, 72]` *(−10%/год)* |
| `19:00` | `10%` | `3800K` | `[100, 79, 63]` *(−10%/год)* |
| `20:00` | `0%` | `3400K` | `[100, 75, 54]` *(мінімум + максимум тепла)* |

> Ви можете будь-коли переглянути свою поточну криву командою `moonlight preview`.

---

## Передумови на моніторі (Gigabyte M27UP)

1. В OSD-меню монітора (джойстик на задній панелі) увімкніть: **Settings → DDC/CI → ON**.
2. Вимкніть вбудоване нічне світло ОС (**Windows Night Light** / **macOS Night Shift**), щоб воно не накладалось поверх гами `moonlight`.
3. На Windows при увімкненому **HDR** прошивка Gigabyte часто блокує апаратну яскравість DDC/CI — для роботи `moonlight` використовуйте SDR-режим.
4. На **macOS (Apple Silicon M1–M4)** підключайте монітор напряму кабелем **USB-C (DP Alt Mode)** або **USB-C → DisplayPort** і встановіть `m1ddc`:
   ```bash
   brew install m1ddc
   ```
   *(На Intel Mac: `brew install ddcctl`.)*

---

## Встановлення та запуск

### 1. Збірка бінарників під обидві платформи

**З Windows (PowerShell):**
```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\build-all.ps1
```

**З macOS / Linux / Git Bash:**
```bash
make test
make build-all
```

У папці `dist/` з'являться готові бінарники:
- `dist/moonlight-windows-amd64.exe`
- `dist/moonlight-darwin-arm64` *(Apple Silicon MacBook)*
- `dist/moonlight-darwin-amd64` *(Intel Mac)*

---

### 2. Налаштування на Windows

```powershell
# 1. Перевірити, що монітор відповідає по DDC/CI
.\dist\moonlight-windows-amd64.exe probe

# 2. Створити config.json (%APPDATA%\moonlight\config.json)
.\dist\moonlight-windows-amd64.exe init

# 3. Перевірити криву та зробити тестовий dry-run без запису
.\dist\moonlight-windows-amd64.exe preview
.\dist\moonlight-windows-amd64.exe apply -dry-run

# 4. Встановити прихований автозапуск у Task Scheduler і запустити демон
powershell -ExecutionPolicy Bypass -File .\scripts\install-windows-task.ps1
```

---

### 3. Налаштування на macOS

```bash
# 1. Встановити DDC/CI утиліту для Apple Silicon
brew install m1ddc

# 2. Скопіювати бінарник у PATH
sudo cp dist/moonlight-darwin-arm64 /usr/local/bin/moonlight
sudo chmod +x /usr/local/bin/moonlight

# 3. Перевірити монітор і створити конфіг (або скопіювати свій config.json з Windows)
moonlight probe
moonlight init

# 4. Поставити автозапуск через LaunchAgent
chmod +x scripts/install-macos-agent.sh
./scripts/install-macos-agent.sh /usr/local/bin/moonlight
```

Логи на macOS пишуться у `~/Library/Logs/moonlight/stdout.log`.

---

## Команди CLI

| Команда | Призначення |
|---|---|
| `moonlight init [-force]` | Створити `config.json` та інтерактивно вибрати монітор при першому запуску |
| `moonlight select [-no-apply]` | Просканувати монітори, вибрати один/кілька/всі та зберегти вибір (змінити монітор у будь-який момент) |
| `moonlight probe` | Показати підключені монітори та їхні поточні VCP-значення (`0x10`, `0x14`, `0x16/0x18/0x1A`) |
| `moonlight preview` | Вивести 24-годинну таблицю яскравості й температури |
| `moonlight status` | Показати активний бекенд, режим нічного світла, паузу, цільові значення та знайдені монітори |
| `moonlight apply [-dry-run]` | Одноразово застосувати поточну точку кривої (з `-dry-run` — тільки показати без запису) |
| `moonlight set -brightness 30 -temp 4000 [-pause 2h] [-confirm]` | Ручне налаштування (`-confirm` авто-відкотить за 12 с, якщо не натиснути Enter) |
| `moonlight pause [2h]` | Поставити демон на паузу на вказаний час (або безстроково без аргументу) |
| `moonlight resume` | Зняти паузу й одразу застосувати поточний розклад |
| `moonlight reset` | **Аварійний скид**: пауза демона + безпечна яскравість і нейтральна гама/RGB на **всіх** моніторах |
| `moonlight restore` | Пауза демона + повернення початкового бекапу з `backup.json` |
| `moonlight daemon [-dry-run]` | Запустити фоновий цикл |

До будь-якої команди можна передати кастомний шлях до конфігу:
```bash
moonlight -config /path/to/config.json status
```

---

## Конфігурація (`config.json`)

Приклад у [`config.example.json`](config.example.json):

```json
{
  "monitor_match": "M27UP,GBT273E",
  "poll_interval_sec": 60,
  "command_delay_ms": 60,
  "retries": 2,
  "min_delta": 1,
  "min_brightness": 0,
  "allow_zero_brightness": true,
  "min_rgb": 25,
  "safe_brightness": 50,
  "enable_brightness": true,
  "enable_night_light": true,
  "night_light_mode": "gamma",
  "color_preset_user": 0,
  "rgb_neutral": [100, 100, 100],
  "ddc_tool": "",
  "schedule": [
    { "time": "09:30", "brightness": 0, "temp_k": 3400 },
    { "time": "10:00", "brightness": 70, "temp_k": 6500 },
    { "time": "12:00", "brightness": 60, "temp_k": 6200 },
    { "time": "14:00", "brightness": 50, "temp_k": 5800 },
    { "time": "16:00", "brightness": 40, "temp_k": 5300 },
    { "time": "17:00", "brightness": 30, "temp_k": 4800 },
    { "time": "18:00", "brightness": 20, "temp_k": 4300 },
    { "time": "19:00", "brightness": 10, "temp_k": 3800 },
    { "time": "20:00", "brightness": 0, "temp_k": 3400 }
  ]
}
```

### Основні параметри
- `monitor_match` — підрядки через кому для фільтрації моніторів (`""` = усі DDC-монітори).
- `night_light_mode` — `"gamma"` (рекомендовано для Gigabyte M27UP), `"ddc"` або `"both"`.
- `min_brightness` / `allow_zero_brightness` — захист від випадкового `0%` (якщо `allow_zero_brightness: false`, `0` заблоковано).
- `min_rgb` — нижня межа для кожного каналу RGB (мінімум `10`, дефолт `25`) — виключає чорний екран.
- `safe_brightness` — яскравість, на яку повертає `moonlight reset`.
- `schedule[].temp_k` — температура в Кельвінах (`1500..10000`); замість неї можна вказати точний `"rgb": [100, 82, 66]`.

---

## Захист від «чорного екрана» та аварійне відновлення

1. **Подвійний захист (валідація + clamp):**
   - При hot-reload кривий `config.json` ігнорується, демон лишається на попередньому робочому.
   - Перед кожним записом спрацьовує `ClampTarget` (яскравість і RGB не можуть вийти за безпечні межі).
2. **Автоматичний бекап (`backup.json`):**
   - Перед першою зміною монітора зберігаються його початкові значення; `moonlight restore` повертає їх назад.
3. **Тестування з авто-відкатом:**
   ```powershell
   moonlight set -brightness 0 -temp 3400 -confirm
   ```
   Якщо не натиснути `Enter` за 12 секунд — усе автоматично повертається назад.
4. **Миттєвий аварійний скид (`moonlight reset`):**
   - Працює навіть якщо `config.json` зламаний, ігнорує `monitor_match`, ставить демон на паузу і скидає яскравість та гаму на всіх моніторах.
   - Готові скрипти порятунку:
     - Windows: [`scripts/rescue-windows.bat`](scripts/rescue-windows.bat) *(можна винести ярлик на робочий стіл і призначити хоткей, наприклад `Ctrl+Alt+R`)*
     - macOS: [`scripts/rescue-macos.command`](scripts/rescue-macos.command)

---

## Структура репозиторію

```text
moonlight/
├── cmd/moonlight/main.go            # CLI та точка входу демона
├── internal/
│   ├── adapter/                     # Автопошук моніторів, профілі виробників, інтерактивний вибір + тести
│   ├── config/                      # Конфіг, валідація, state.json, backup.json + тести
│   ├── schedule/                    # 24-годинна інтерполяція та Kelvin -> RGB + тести
│   ├── ddc/                         # DDC/CI бекенди (Windows dxva2 / macOS m1ddc, ddcctl)
│   ├── gamma/                       # Нативна гама нічного світла (Windows gdi32 / macOS CoreGraphics)
│   └── daemon/                      # Фоновий цикл, hot-reload, KVM-логіка, reset/restore
├── scripts/
│   ├── build-all.ps1                # Кросплатформна збірка з Windows
│   ├── install-windows-task.ps1     # Автозапуск Windows (Task Scheduler)
│   ├── run-daemon-hidden.vbs        # Запуск без вікна консолі на Windows
│   ├── install-macos-agent.sh       # Автозапуск macOS (LaunchAgent)
│   ├── rescue-windows.bat           # Аварійний скид (Windows)
│   └── rescue-macos.command         # Аварійний скид (macOS)
├── config.example.json
├── Makefile
└── go.mod
```
