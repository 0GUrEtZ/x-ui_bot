# 3x-ui Telegram Bot

Telegram бот для управления 3x-ui панелью через API.

## Быстрый старт

1. **Получить токен:** [@BotFather](https://t.me/BotFather) → `/newbot`
2. **Узнать ID:** [@userinfobot](https://t.me/userinfobot)
3. **Настроить:**
```bash
cp .env.example .env
nano .env
```

Минимум в `.env`:
```env
PANEL_URL=http://3x-ui:2053
PANEL_USERNAME=admin
PANEL_PASSWORD=your_password
TG_BOT_TOKEN=your_token
TG_BOT_ADMIN_IDS=your_id
```

4. **Запустить:**
```bash
docker-compose up -d
```

## Команды

- `/start` `/help` `/id`
- `/status` - Статус сервера
- `/usage <email>` - Статистика клиента

## Переменные .env

**Обязательные:** `PANEL_URL`, `PANEL_USERNAME`, `PANEL_PASSWORD`, `TG_BOT_TOKEN`, `TG_BOT_ADMIN_IDS`

**Опциональные:** `TG_BOT_PROXY`, `TG_BOT_API_SERVER`

## Проблемы

```bash
docker-compose logs -f x-ui-bot
```

---
Основано на [3x-ui](https://github.com/MHSanaei/3x-ui)
