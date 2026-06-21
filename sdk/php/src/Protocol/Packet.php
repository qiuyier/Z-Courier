<?php

declare(strict_types=1);

namespace ZCourier\Protocol;

final readonly class Packet
{
    public const MAGIC = 0x5A43;
    public const VERSION = 1;
    public const FIXED_HEADER_SIZE = 41;
    public const DEFAULT_MAX_BODY_SIZE = 4 << 20;

    public const MSG_ID_ACK = 1;
    public const MSG_ID_DOWNLINK_ACK = 2;
    public const MSG_ID_BIND = 1000;

    public const FLAG_ACK_REQUIRED = 1 << 0;
    public const FLAG_COMPRESSED = 1 << 1;

    public string $sequence;
    public string $timestamp;

    public function __construct(
        public int $msgId,
        public string $body = '',
        public int $version = self::VERSION,
        public int $flags = 0,
        int|string $sequence = '0',
        int|string $timestamp = '0',
        public string $clientId = '',
        public string $deviceId = '',
        public string $sessionId = '',
        public string $messageId = '',
        public string $traceId = '',
        public string $token = '',
    ) {
        $this->sequence = Integer64::normalizeUnsigned($sequence);
        $this->timestamp = Integer64::normalizeSigned($timestamp);
    }

    public static function isReservedMsgId(int $msgId): bool
    {
        return in_array($msgId, [self::MSG_ID_ACK, self::MSG_ID_DOWNLINK_ACK, self::MSG_ID_BIND], true);
    }
}
