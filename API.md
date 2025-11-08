# 3x-ui API Documentation

Документация по API панели 3x-ui для взаимодействия с сервером.

## Аутентификация

**Endpoint:** `/login`  
**Method:** `POST`

```json
{
  "username": "admin",
  "password": "password"
}
```

Возвращает Cookie с `session_id` для последующих запросов.

---

## API Inbounds

**Base Path:** `/panel/api/inbounds`

### Основные методы

| Method | Endpoint | Описание |
|--------|----------|----------|
| GET | `/list` | Получить все inbound'ы |
| GET | `/get/:id` | Получить inbound по ID |
| GET | `/getClientTraffics/:email` | Получить трафик клиента по email |
| GET | `/getClientTrafficsById/:id` | Получить трафик всех клиентов inbound'а |
| POST | `/add` | Добавить новый inbound |
| POST | `/del/:id` | Удалить inbound |
| POST | `/update/:id` | Обновить inbound |
| POST | `/addClient` | Добавить клиента в inbound |
| POST | `/:id/delClient/:clientId` | Удалить клиента |
| POST | `/updateClient/:clientId` | Обновить клиента |
| POST | `/:id/resetClientTraffic/:email` | Сбросить трафик клиента |
| POST | `/resetAllTraffics` | Сбросить трафик всех inbound'ов |
| POST | `/resetAllClientTraffics/:id` | Сбросить трафик всех клиентов |
| POST | `/delDepletedClients/:id` | Удалить клиентов с исчерпанным трафиком |
| POST | `/onlines` | Получить список онлайн клиентов |

### Примеры ответов

#### GET /list
```json
{
  "success": true,
  "msg": "",
  "obj": [
    {
      "id": 1,
      "up": 0,
      "down": 0,
      "total": 0,
      "remark": "VLESS-REALITY",
      "enable": true,
      "expiryTime": 0,
      "listen": "",
      "port": 443,
      "protocol": "vless",
      "settings": "{\"clients\":[...],\"decryption\":\"none\"}",
      "streamSettings": "{\"network\":\"tcp\",\"security\":\"reality\",...}",
      "tag": "inbound-443",
      "sniffing": "{\"enabled\":true,\"destOverride\":[\"http\",\"tls\"]}"
    }
  ]
}
```

#### GET /getClientTrafficsById/:id
```json
{
  "success": true,
  "msg": "",
  "obj": [
    {
      "id": 1,
      "inboundId": 1,
      "enable": true,
      "email": "client@example.com",
      "up": 1048576000,
      "down": 2097152000,
      "expiryTime": 1735689600000,
      "total": 107374182400,
      "reset": 0
    }
  ]
}
```

---

## API Server

**Base Path:** `/panel/api/server`

| Method | Endpoint | Описание |
|--------|----------|----------|
| GET | `/status` | Получить статус сервера |
| GET | `/getXrayVersion` | Получить версии Xray |
| GET | `/getConfigJson` | Скачать config.json |
| GET | `/getDb` | Скачать базу данных |
| GET | `/getNewUUID` | Сгенерировать UUID |
| POST | `/stopXrayService` | Остановить Xray |
| POST | `/restartXrayService` | Перезапустить Xray |
| POST | `/logs/:count` | Получить системные логи |

### Пример ответа статуса

```json
{
  "success": true,
  "obj": {
    "cpu": 15.5,
    "mem": {
      "current": 1024000000,
      "total": 8192000000
    },
    "swap": {
      "current": 0,
      "total": 2048000000
    },
    "disk": {
      "current": 10240000000,
      "total": 51200000000
    },
    "xray": {
      "state": "running",
      "version": "1.8.10"
    },
    "uptime": 86400,
    "loads": [1.5, 1.3, 1.1]
  }
}
```

---

## Структура данных клиента

```json
{
  "id": "uuid-string",
  "email": "client@example.com",
  "enable": true,
  "flow": "",
  "limitIp": 0,
  "totalGB": 100,
  "expiryTime": 1735689600000,
  "tgId": "",
  "subId": "",
  "reset": 0
}
```

### Поля клиента

- `id` - UUID клиента (VLESS/VMESS) или password (Trojan)
- `email` - уникальный email клиента
- `enable` - статус активности
- `totalGB` - лимит трафика в GB (0 = безлимит)
- `expiryTime` - дата истечения в миллисекундах Unix timestamp (0 = бессрочный)
- `limitIp` - лимит одновременных IP (0 = без лимита)

---

## Полезные ссылки

- [GitHub Wiki](https://github.com/MHSanaei/3x-ui/wiki/Configuration#api-documentation)
- [Postman Collection](https://www.postman.com/hsanaei/3x-ui/documentation/q1l5l0u/3x-ui)
- [Run in Postman](https://app.getpostman.com/run-collection/5146551-dda3cab3-0e33-485f-96f9-d4262f437ac5)
