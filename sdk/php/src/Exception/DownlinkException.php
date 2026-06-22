<?php

declare(strict_types=1);

namespace ZCourier\Exception;

use Throwable;

final class DownlinkException extends ClientException
{
    public function __construct(
        string $kind,
        public readonly string $operation,
        public readonly int $msgId,
        public readonly string $messageId,
        string $message,
        ?Throwable $previous = null,
    ) {
        parent::__construct($kind, $message, $previous);
    }
}
