# План реимплементации OpenCode Dynamic Context Pruning (DCP) в SMAGo

## 1. Краткое описание проектов

### SMAGo (текущий проект)
- **Язык:** Go | **Интерфейс:** Telegram | **Само-модифицирующийся AI-агент**
- Хранение сессий: SQLite (`sessions.db`), массив `[]ChatMessage`
- Agent Loop: `Handle()` в `agent.go` — собирает messages → вызывает LLM → выполняет tools → повторяет
- **Проблема:** Нет управления контекстом. При длинных сессиях контекст раздувается → растут токены, падает качество

### OpenCode DCP
- TypeScript-плагин для OpenCode. Ядро: dynamic context pruning
- 3 механизма: **Compress** (LLM сам сжимает), **Dedup** (дубли tools), **Purge Errors** (ошибки)
- Nudge-система: напоминания LLM про compress при подходе к лимиту

---

## 2. Архитектурные отличия

| OpenCode DCP | SMAGo |
|---|---|
| Plugin-система, хуки `messages.transform` | Нативный Go-код в `agent.go` |
| Rich `WithParts[]` (text/tool/reasoning parts) | Простой `ChatMessage{Role, Content, ToolCalls}` |
| Anthropic tokenizer | `Usage.PromptTokens` из API + `len/4` fallback |
| State в файлах | SQLite (расширить `sessions.db`) |
| Multi-session с sessionID | Одна сессия на `chatID` |
| Message IDs `m0001`, `b2` | Индексы массива `[]ChatMessage` |

**Ключевое архитектурное решение:**
В SMAGo **оригинальные сообщения НЕ модифицируются**. Они хранятся в SQLite как есть. Перед каждым LLM-вызовом собирается «виртуальный» массив messages с применёнными сжатиями.

---

## 3. Компоненты DCP

### 3.1 DCPState — состояние DCP
**Новый файл:** `dcp_state.go`

```go
type DCPState struct {
    CompressedRanges  []CompressedRange  // Активные блоки сжатия
    SeenToolCalls     map[string]int     // signature → messageIndex (для dedup)
    ErrorToolCalls    []ErrorToolEntry   // Ошибки для purge
    LastNudgeStep     int                // Последний шаг с нуджем
    LastCompressStep  int                // Последний compress
    TotalPrunedTokens int                // Экономия токенов
    CurrentTurn       int                // Текущий ход
    CurrentTokens     int                // Текущие токены (из usage)
}

type CompressedRange struct {
    ID            int       // Уникальный ID блока
    StartIdx      int       // Индекс первого сообщения (в оригинальной сессии)
    EndIdx        int       // Индекс последнего
    Summary       string    // Саммари от LLM
    SummaryTokens int
    Topic         string    // Краткая метка
    Active        bool
    CreatedAt     time.Time
}

type ErrorToolEntry struct {
    ToolCallID string
    MessageIdx int
    Turn       int
}
```

### 3.2 Token Estimation
**Новый файл:** `dcp_token.go`

```go
func EstimateTokens(text string) int        // ~len(text)/4
func CountMessageTokens(msg ChatMessage) int // content + args
func CountAllMessagesTokens(msgs []ChatMessage) int
```

SMAGo уже получает `Usage.PromptTokens` из API — используем как основной источник, `EstimateTokens` как fallback.

### 3.3 Config расширение
**Изменённый файл:** `config.go`

```go
type DCPConfig struct {
    Enabled            bool `json:"dcpEnabled"`
    MaxContextTokens   int  `json:"dcpMaxContextTokens"`   // 80000
    MinContextTokens   int  `json:"dcpMinContextTokens"`   // 40000
    NudgeFrequency     int  `json:"dcpNudgeFrequency"`     // 5
    PurgeErrorsTurns   int  `json:"dcpPurgeErrorsTurns"`   // 4
    ProtectRecentCount int  `json:"dcpProtectRecentCount"` // 4
    ShowNotifications  bool `json:"dcpShowNotifications"`
    ManualMode         bool `json:"dcpManualMode"`
}
```

---

## 4. Три стратегии DCP

### 4.1 Deduplication
**Новый файл:** `dcp_strategies.go`

**Алгоритм:**
1. При tool-вызове: `signature = toolName::sorted(argsJSON)` → сохранить в `SeenToolCalls[signature] = msgIdx`
2. При построении messages: если `signature` уже встречалась с меньшим индексом → заменить Content на `"[Duplicate removed]"`
3. **Защита:** не удалять `terminal`, `write_file`, `edit_file`, `self_modify`, `compress`

### 4.2 Purge Errors
**Новый файл:** `dcp_strategies.go`

**Алгоритм:**
1. При ошибке tool-вызова → записать в `ErrorToolCalls`
2. Через `PurgeErrorsTurns` ходов: Content ошибочного tool-сообщения → `"[Error input removed to save context]"`
3. Сохраняем саму ошибку, удаляем только входные данные

### 4.3 Compress (ядро)
**Новый файл:** `dcp_compress.go`

**Новый tool для LLM:**
```
Name: "compress"
Description: Replace closed/old conversation ranges with summaries.
Parameters: { topic, ranges: [{start_idx, end_idx, summary}] }
```

**Execute:**
1. Валидация диапазонов
2. Создание `CompressedRange` в `DCPState`
3. Возврат `"Compressed N messages into summary (X tokens saved)"`

---

## 5. Nudge System
**Новый файл:** `dcp_nudges.go`

### Context Limit Nudge
Когда `CurrentTokens > MaxContextTokens`:
```
"[SYSTEM] Context approaching limit. Use 'compress' tool to summarize old conversation ranges."
```

### Turn Nudge  
Когда `CurrentTokens > MinContextTokens` и прошло `NudgeFrequency` шагов:
```
"[SYSTEM] Consider compressing completed sections to free context."
```

Впрыскиваются как system-сообщение перед отправкой в LLM.

---

## 6. Изменения в Agent Loop

### 6.1 Новый метод `buildDCPMessages()`
**Файл:** `agent.go`

```go
func (a *Agent) buildDCPMessages(sess *Session, dcp *DCPState, step int) []ChatMessage {
    msgs := sess.Messages()  // Оригинал из SQLite
    
    // 1. Применить сжатия: заменить сжатые диапазоны на synthetic summary-сообщения
    msgs = applyCompression(msgs, dcp)
    
    // 2. Deduplication: заменить дубли
    msgs = deduplicateToolCalls(msgs, dcp)
    
    // 3. Purge errors: заменить старые ошибки
    msgs = purgeErrorInputs(msgs, dcp)
    
    // 4. Nudge injection
    msgs = injectNudges(msgs, dcp, step)
    
    // 5. Добавить system prompt
    result := []ChatMessage{{Role: "system", Content: a.cfg.SystemPrompt}}
    result = append(result, msgs...)
    
    return result
}
```

### 6.2 Изменения в `Handle()`
```go
// Внутри for-loop, после получения tool results:
if tc.Function.Name == "compress" {
    a.handleCompressResult(dcpState, args, result)
    a.recordTrace(chatID, "📦 DCP: "+result)
    continue
}
```

### 6.3 Compress Tool Registration
**Файл:** `tools.go` — в `registerDefaults()`:

```go
r.tools["compress"] = ToolDef{
    Name:        "compress",
    Description: "Replace closed/old conversation ranges with detailed summaries...",
    Parameters:  compressSchema,
    Execute:     r.execCompress,
}
```

---

## 7. SQLite Persistence
**Файл:** `session.go`

```sql
CREATE TABLE IF NOT EXISTS dcp_state (
    chat_id INTEGER PRIMARY KEY,
    state TEXT NOT NULL,    -- JSON serialized DCPState
    updated_at INTEGER NOT NULL
);
```

```go
func (s *Store) LoadDCPState(chatID int64) (*DCPState, error)
func (s *Store) SaveDCPState(chatID int64, state *DCPState) error
```

---

## 8. Telegram-команды

```
/dcp          — Статистика: токены сейчас, экономия, кол-во сжатий
/dcp compress — Ручное сжатие (принудительно)
/dcp on       — Включить DCP
/dcp off      — Выключить DCP
```

---

## 9. Порядок реализации

| # | Модуль | Файл | Сложность |
|---|---|---|---|
| 1 | Типы данных | `dcp_state.go` | Низкая |
| 2 | Token utils | `dcp_token.go` | Низкая |
| 3 | Config | `config.go` (extend) | Низкая |
| 4 | SQLite persistence | `session.go` (extend) | Низкая |
| 5 | Dedup + Purge | `dcp_strategies.go` | Средняя |
| 6 | Интеграция стратегий | `agent.go` (extend) | Средняя |
| 7 | Compress tool | `dcp_compress.go` | Средняя |
| 8 | buildDCPMessages | `dcp.go` | Высокая |
| 9 | Nudge system | `dcp_nudges.go` | Средняя |
| 10 | Полная интеграция | `agent.go` | Средняя |
| 11 | Telegram команды | `agent.go` (extend) | Низкая |
| 12 | Тестирование | — | Средняя |

**Оценка:** ~8 часов

---

## 10. Ключевые решения

1. **Оригиналы не модифицируются** — virtual messages перед LLM
2. **No external tokenizer** — `Usage.PromptTokens` + `len/4`
3. **Compress = LLM сам решает** — получает tool, сам генерирует summary
4. **Защищённые tools** — terminal, write_file, edit_file, self_modify, compress
5. **Простые индексы** вместо message ID — `start_idx/end_idx` в сессии
