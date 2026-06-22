<?php

declare(strict_types=1);

namespace ZCourier\Protocol;

use JsonException;
use ZCourier\Exception\ProtocolException;

final readonly class DeliveryAck
{
    public const DELIVERED = 'delivered';

    public function __construct(
        public string $messageId,
        public string $code = self::DELIVERED,
    ) {
        if ($this->messageId === '' || $this->code !== self::DELIVERED) {
            throw new ProtocolException(
                ProtocolException::INVALID_DELIVERY_ACK,
                'delivery ACK fields are invalid',
            );
        }
    }

    public static function fromPacket(Packet $packet): self
    {
        if ($packet->msgId !== Packet::MSG_ID_DOWNLINK_ACK) {
            throw new ProtocolException(
                ProtocolException::INVALID_DELIVERY_ACK,
                'packet is not a delivery ACK',
            );
        }
        try {
            $data = json_decode($packet->body, true, 512, JSON_THROW_ON_ERROR);
        } catch (JsonException $exception) {
            throw new ProtocolException(
                ProtocolException::INVALID_DELIVERY_ACK,
                'invalid delivery ACK JSON: ' . $exception->getMessage(),
            );
        }
        if (!is_array($data) || !is_string($data['message_id'] ?? null) || !is_string($data['code'] ?? null)) {
            throw new ProtocolException(
                ProtocolException::INVALID_DELIVERY_ACK,
                'delivery ACK fields are invalid',
            );
        }
        return new self($data['message_id'], $data['code']);
    }

    public function toJson(): string
    {
        return json_encode(
            ['message_id' => $this->messageId, 'code' => $this->code],
            JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES,
        );
    }
}
