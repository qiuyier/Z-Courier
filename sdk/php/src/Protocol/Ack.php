<?php

declare(strict_types=1);

namespace ZCourier\Protocol;

use JsonException;
use ZCourier\Exception\ProtocolException;

final readonly class Ack
{
    public const ACCEPTED = 'accepted';
    public const DECODE_FAILED = 'decode_failed';
    public const UNAUTHORIZED = 'unauthorized';
    public const AUTH_UNAVAILABLE = 'auth_unavailable';
    public const REJECTED = 'rejected';

    public function __construct(
        public string $code,
        public int $msgId,
        public string $messageId = '',
        public string $reason = '',
    ) {
        if ($this->code === '') {
            throw new ProtocolException(ProtocolException::INVALID_ACK, 'ACK code is required');
        }
    }

    public static function fromPacket(Packet $packet): self
    {
        if ($packet->msgId !== Packet::MSG_ID_ACK) {
            throw new ProtocolException(ProtocolException::INVALID_ACK, 'packet is not an ACK');
        }
        try {
            $data = json_decode($packet->body, true, 512, JSON_THROW_ON_ERROR);
        } catch (JsonException $exception) {
            throw new ProtocolException(ProtocolException::INVALID_ACK, 'invalid ACK JSON: ' . $exception->getMessage());
        }
        if (!is_array($data) || !is_string($data['code'] ?? null) || !is_int($data['msg_id'] ?? null)) {
            throw new ProtocolException(ProtocolException::INVALID_ACK, 'ACK fields are invalid');
        }
        return new self(
            $data['code'],
            $data['msg_id'],
            is_string($data['message_id'] ?? null) ? $data['message_id'] : '',
            is_string($data['reason'] ?? null) ? $data['reason'] : '',
        );
    }

    public function toJson(): string
    {
        $data = ['code' => $this->code, 'msg_id' => $this->msgId];
        if ($this->messageId !== '') {
            $data['message_id'] = $this->messageId;
        }
        if ($this->reason !== '') {
            $data['reason'] = $this->reason;
        }
        return json_encode($data, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
    }
}
