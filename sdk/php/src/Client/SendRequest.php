<?php

declare(strict_types=1);

namespace ZCourier\Client;

final readonly class SendRequest
{
    public function __construct(
        public int $msgId,
        public string $body = '',
        public string $messageId = '',
        public string $traceId = '',
        public int $flags = 0,
        public bool $ackRequired = false,
    ) {
    }
}
