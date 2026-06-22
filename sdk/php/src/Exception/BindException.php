<?php

declare(strict_types=1);

namespace ZCourier\Exception;

final class BindException extends ClientException
{
    public function __construct(
        string $kind,
        public readonly string $ackCode,
        public readonly string $reason = '',
    ) {
        $message = "bind failed with code {$ackCode}";
        if ($reason !== '') {
            $message .= ": {$reason}";
        }
        parent::__construct($kind, $message);
    }
}
