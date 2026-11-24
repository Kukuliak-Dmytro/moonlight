# moonlight

Кросплатформний Go-демон і CLI для автоматичного апаратного керування **яскравістю** та **нічним світлом (RGB-теплотою)** монітора через **DDC/CI** на **Windows** і **macOS**.

Спроєктований під зв'язку **Gigabyte M27UP (KVM) + Windows PC + MacBook**:
- **Нуль зовнішніх Go-залежностей** (pure stdlib, `CGO_ENABLED=0`) — збирається під Windows і macOS з однієї кодової бази.
- **Плавна 24-годинна крива** з лінійною інтерполяцією між точками часу (включно з переходом через північ).
- **KVM-friendly**: перевідкриває дескриптори монітора на кожному циклі та автоматично перевиставляє яскравість/теплоту, щойно монітор повертається після перемикання KVM чи сну.
- **Hot-reload конфігу**: редагуйте `config.json` без перезапуску демона.

---

## Передумови на моніторі (Gigabyte M27UP)

1. Відкрийте OSD-меню монітора джойстиком: **Settings → DDC/CI → ON**.
2. Для апаратної теплоти (RGB Gain) переконайтеся, що монітор не заблокований у режимі HDR (у Windows при увімкненому HDR DDC/CI яскравість часто блокується) і що колірний режим дозволяє змінювати RGB (зазвичай **Color Temperature → User Define**).
3. На **macOS (Apple Silicon)** встановіть [`m1ddc`](https://github.com/waydabber/m1ddc):
   ```bash
   brew install m1ddc
   ```
   *(На Intel Mac можна встановити `brew install ddcctl`.)*

---

## Збірка під обидві платформи

### З Windows (PowerShell)
```powershell
.\scripts\build-all.ps1
```
Створить готові бінарники в папці `dist/`:
- `dist/moonlight-windows-amd64.exe`
- `dist/moonlight-darwin-arm64` (Apple Silicon MacBook M1–M4)
- `dist/moonlight-darwin-amd64` (Intel Mac)

### З macOS / Linux / Git Bash
```bash
make test
make build-all
```

---

## Швидкий старт

1. **Перевірте, як монітор відповідає по DDC/CI:**
   ```bash
   moonlight probe
   ```
   Команда покаже точну назву монітора та поточні/максимальні значення `Brightness (0x10)`, `Color Preset (0x14)` і `Red/Green/Blue Gain (0x16/0x18/0x1A)`.
   > Якщо `Red/Green/Blue Gain` у нейтральному денному режимі дорівнюють `50 / 100`, вкажіть `"rgb_neutral": [50, 50, 50]` у `config.json`. Якщо `100 / 100` — залиште дефолтні `[100, 100, 100]`.

2. **Створіть `config.json`:**
   ```bash
   moonlight init
   ```
   - Windows: `%APPDATA%\moonlight\config.json`
   - macOS: `~/Library/Application Support/moonlight/config.json`

   *(Можете синхронізувати один і той самий `config.json` між Windows і MacBook!)*

3. **Подивіться розраховану криву на 24 години:**
   ```bash
   moonlight preview
   ```

4. **Застосуйте зараз або запустіть демон:**
   ```bash
   moonlight apply
   moonlight daemon
   ```

---

## Команди CLI

| Команда | Опис |
|---|---|
| `moonlight init [-force]` | Створити дефолтний `config.json` |
| `moonlight probe` | Просканувати монітори та зчитати VCP-коди (яскравість, RGB, пресет) |
| `moonlight preview` | Показати таблицю яскравості й RGB на кожну годину доби |
| `moonlight status` | Показати активний бекенд, статус паузи, цільові значення та знайдені монітори |
| `moonlight apply [-dry-run]` | Одноразово застосувати поточну точку розкладу (або перевірити без запису) |
| `moonlight set -brightness 60 -temp 4500 -confirm` | Ручне налаштування (`-confirm` поверне попередні значення за 12 с, якщо не натиснути Enter) |
| `moonlight reset` | **АВАРІЙНИЙ СКИД**: ставить демон на паузу і виставляє безпечну яскравість (`70%`) та нейтральний RGB на **всіх** моніторах |
| `moonlight restore` | Ставить демон на паузу і повертає початкові апаратні значення з `backup.json` |
| `moonlight pause 2h` | Поставити автооновлення на паузу на 2 години (або без аргументу — безстроково) |
| `moonlight resume` | Зняти паузу й одразу застосувати розклад |
| `moonlight daemon [-dry-run]` | Запустити фоновий цикл |

---

## Захист від «чорного екрана» та аварійне відновлення

1. **Жорсткі нижні ліміти безпеки (`min_brightness`, `min_rgb`)**:
   - За замовчуванням `min_brightness = 5` (яскравість `0` заблокована, поки явно не вказано `"allow_zero_brightness": true`), а `min_rgb = 25` (неможливо випадково записати `[0, 0, 0]`).
   - Ліміти перевіряються **двічі**: при завантаженні/hot-reload `config.json` і безпосередньо перед відправкою DDC/CI-команди. Якщо зробити помилку в конфігу — демон просто відхилить його і залишить попередній робочий.
2. **Автоматичний бекап (`backup.json`)**:
   - Перед першим записом на монітор `moonlight` зчитує його поточні значення яскравості та RGB і зберігає в `backup.json`. Команда `moonlight restore` повертає їх назад.
3. **Тестування без ризику (`-dry-run` і `-confirm`)**:
   - `moonlight apply -dry-run` покаже, що саме планується записати, нічого не змінюючи на моніторі.
   - `moonlight set -brightness 15 -temp 3200 -confirm` застосує налаштування і дасть **12 секунд** на підтвердження через `Enter` — якщо екран не читається і ви не натиснете `Enter`, все автоматично відкотиться назад.
4. **Миттєвий аварійний скид (`moonlight reset`)**:
   - Працює навіть якщо `config.json` зламаний, ігнорує `monitor_match`, ставить демон на паузу й повертає `70%` яскравості та нейтральний RGB.
   - Для швидкого доступу є готові скрипти: `scripts/rescue-windows.bat` (можна винести ярлик на робочий стіл і повісити хоткей на кшталт `Ctrl+Alt+R`) та `scripts/rescue-macos.command`.
   - І пам'ятайте: DDC/CI ніколи не вимикає живлення монітора — у крайньому разі джойстик на задній панелі **Gigabyte M27UP** завжди дозволяє скинути налаштування через OSD (`Reset All`).

---

## Автозапуск

### Windows (Task Scheduler)
```powershell
.\scripts\install-windows-task.ps1
```

### macOS (LaunchAgent)
```bash
sudo cp dist/moonlight-darwin-arm64 /usr/local/bin/moonlight
sudo chmod +x /usr/local/bin/moonlight
./scripts/install-macos-agent.sh /usr/local/bin/moonlight
```

---

## Приклад `config.json`

Див. [`config.example.json`](config.example.json). Ви можете вказувати температуру в Кельвінах (`"temp_k": 3800`) або точні RGB-значення (`"rgb": [100, 78, 56]`) для кожної точки кривої.
