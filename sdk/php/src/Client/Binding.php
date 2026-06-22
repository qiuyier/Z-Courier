<?php

declare(strict_types=1);

namespace ZCourier\Client;

final readonly class Binding
{
    public function __construct(
        public string $clientId,
        public string $deviceId,
        public string $sessionId,
    ) {
    }
}
