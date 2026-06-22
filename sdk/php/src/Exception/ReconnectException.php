<?php

declare(strict_types=1);

namespace ZCourier\Exception;

use Throwable;

final class ReconnectException extends ClientException
{
    public function __construct(
        public readonly int $attempts,
        Throwable $previous,
    ) {
        parent::__construct(
            ClientException::RECONNECT_EXHAUSTED,
            "reconnect attempts exhausted after {$attempts} attempts: {$previous->getMessage()}",
            $previous,
        );
    }
}
