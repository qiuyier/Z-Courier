<?php

declare(strict_types=1);

namespace ZCourier\Exception;

use RuntimeException;

final class ProtocolException extends RuntimeException
{
    public const PACKET_TOO_SHORT = 'packet_too_short';
    public const INVALID_MAGIC = 'invalid_magic';
    public const UNSUPPORTED_VERSION = 'unsupported_version';
    public const LENGTH_MISMATCH = 'length_mismatch';
    public const BODY_TOO_LARGE = 'body_too_large';
    public const FIELD_TOO_LARGE = 'field_too_large';
    public const FRAME_TOO_SHORT = 'frame_too_short';
    public const FRAME_TOO_LARGE = 'frame_too_large';
    public const FRAME_LENGTH_MISMATCH = 'frame_length_mismatch';
    public const OUTER_INNER_MSG_ID_MISMATCH = 'outer_inner_msg_id_mismatch';
    public const INVALID_ACK = 'invalid_ack';
    public const INVALID_DELIVERY_ACK = 'invalid_delivery_ack';
    public const INVALID_INTEGER = 'invalid_integer';

    public function __construct(
        public readonly string $kind,
        string $message,
    ) {
        parent::__construct($message);
    }
}
