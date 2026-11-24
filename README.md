# monlight

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
- `dist/monlight-windows-amd64.exe`
- `dist/monlight-darwin-arm64` (Apple Silicon MacBook M1–M4)
- `dist/monlight-darwin-amd64` (Intel Mac)

### З macOS / Linux / Git Bash
```bash
make test
make build-all
```

---

## Швидкий старт

1. **Перевірте, як монітор відповідає по DDC/CI:**
   ```bash
   monlight probe
   ```
   Команда покаже точну назву монітора та поточні/максимальні значення `Brightness (0x10)`, `Color Preset (0x14)` і `Red/Green/Blue Gain (0x16/0x18/0x1A)`.
   > Якщо `Red/Green/Blue Gain` у нейтральному денному режимі дорівнюють `50 / 100`, вкажіть `"rgb_neutral": [50, 50, 50]` у `config.json`. Якщо `100 / 100` — залиште дефолтні `[100, 100, 100]`.

2. **Створіть `config.json`:**
   ```bash
   monlight init
   ```
   - Windows: `%APPDATA%\monlight\config.json`
   - macOS: `~/Library/Application Support/monlight/config.json`

   *(Можете синхронізувати один і той самий `config.json` між Windows і MacBook!)*

3. **Подивіться розраховану криву на 24 години:**
   ```bash
   monlight preview
   ```

4. **Застосуйте зараз або запустіть демон:**
   ```bash
   monlight apply
   monlight daemon
   ```

---

## Команди CLI

| Команда | Опис |
|---|---|
| `monlight init [-force]` | Створити дефолтний `config.json` |
| `monlight probe` | Просканувати монітори та зчитати VCP-коди (яскравість, RGB, пресет) |
| `monlight preview` | Показати таблицю яскравості й RGB на кожну годину доби |
| `monlight status` | Показати активний бекенд, статус паузи, цільові значення та знайдені монітори |
| `monlight apply` | Одноразово застосувати поточну точку розкладу |
| `monlight set -brightness 60 -temp 4500 -pause 2h` | Ручне налаштування (+ опціональна пауза демона) |
| `monlight pause 2h` | Поставити автооновлення на паузу на 2 години (або без аргументу — безстроково) |
| `monlight resume` | Зняти паузу й одразу застосувати розклад |
| `monlight daemon` | Запустити фоновий цикл |

---

## Автозапуск

### Windows (Task Scheduler)
```powershell
.\scripts\install-windows-task.ps1
```

### macOS (LaunchAgent)
```bash
sudo cp dist/monlight-darwin-arm64 /usr/local/bin/monlight
sudo chmod +x /usr/local/bin/monlight
./scripts/install-macos-agent.sh /usr/local/bin/monlight
```

---

## Приклад `config.json`

Див. [`config.example.json`](config.example.json). Ви можете вказувати температуру в Кельвінах (`"temp_k": 3800`) або точні RGB-значення (`"rgb": [100, 78, 56]`) для кожної точки кривої.
