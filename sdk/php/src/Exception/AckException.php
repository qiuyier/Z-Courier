<?php

declare(strict_types=1);

namespace ZCourier\Exception;

use ZCourier\Protocol\Ack;

final class AckException extends ClientException
{
    public function __construct(
        string $kind,
        public readonly Ack $ack,
    ) {
        $message = "ACK failed with code {$ack->code}";
        if ($ack->reason !== '') {
            $message .= ": {$ack->reason}";
        }
        parent::__construct($kind, $message);
    }
}
