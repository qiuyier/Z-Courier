<?php

declare(strict_types=1);

namespace ZCourier\Client;

use ZCourier\Protocol\Ack;

final readonly class SendResult
{
    public function __construct(
        public string $messageId,
        public ?Ack $ack = null,
    ) {
    }
}
