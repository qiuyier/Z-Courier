package downlink

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func normalizeMessageListQuery(query MessageListQuery) MessageListQuery {
	if query.Limit <= 0 {
		query.Limit = defaultMessageListLimit
	}
	if query.Limit > maxMessageListLimit {
		query.Limit = maxMessageListLimit
	}
	query.Cursor = normalizeMessageListCursor(query.Cursor)
	return query
}

func normalizeMessageListCursor(cursor MessageListCursor) MessageListCursor {
	cursor.MessageID = strings.TrimSpace(cursor.MessageID)
	if cursor.MessageID == "" || cursor.UpdatedAt.IsZero() {
		return MessageListCursor{}
	}
	cursor.UpdatedAt = cursor.UpdatedAt.UTC()
	return cursor
}

func messageListCursorFromMessage(message Message) MessageListCursor {
	return normalizeMessageListCursor(MessageListCursor{
		UpdatedAt: retentionTime(message),
		MessageID: message.MessageID,
	})
}

func messageListCursorIsZero(cursor MessageListCursor) bool {
	return cursor.MessageID == "" || cursor.UpdatedAt.IsZero()
}

func messageAfterListCursor(message Message, cursor MessageListCursor) bool {
	cursor = normalizeMessageListCursor(cursor)
	if messageListCursorIsZero(cursor) {
		return true
	}

	updatedAt := retentionTime(message).UTC()
	if updatedAt.Before(cursor.UpdatedAt) {
		return true
	}
	if updatedAt.After(cursor.UpdatedAt) {
		return false
	}
	return message.MessageID > cursor.MessageID
}

func formatMessageListCursor(cursor MessageListCursor) string {
	cursor = normalizeMessageListCursor(cursor)
	if messageListCursorIsZero(cursor) {
		return ""
	}

	messageID := base64.RawURLEncoding.EncodeToString([]byte(cursor.MessageID))
	return fmt.Sprintf("%d:%s", cursor.UpdatedAt.UnixNano(), messageID)
}

func parseMessageListCursor(raw string) (MessageListCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return MessageListCursor{}, nil
	}

	updatedRaw, messageIDRaw, ok := strings.Cut(raw, ":")
	if !ok || updatedRaw == "" || messageIDRaw == "" {
		return MessageListCursor{}, ErrInvalidCursor
	}
	updatedUnixNano, err := strconv.ParseInt(updatedRaw, 10, 64)
	if err != nil || updatedUnixNano <= 0 {
		return MessageListCursor{}, ErrInvalidCursor
	}
	messageIDBytes, err := base64.RawURLEncoding.DecodeString(messageIDRaw)
	if err != nil || len(messageIDBytes) == 0 {
		return MessageListCursor{}, ErrInvalidCursor
	}

	return normalizeMessageListCursor(MessageListCursor{
		UpdatedAt: time.Unix(0, updatedUnixNano).UTC(),
		MessageID: string(messageIDBytes),
	}), nil
}
