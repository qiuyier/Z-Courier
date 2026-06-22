<?php

declare(strict_types=1);

namespace ZCourier\Exception;

use RuntimeException;

class ClientException extends RuntimeException
{
    public const INVALID_CONFIG = 'invalid_config';
    public const CLOSED = 'closed';
    public const CONNECT_FAILED = 'connect_failed';
    public const TOKEN_UNAVAILABLE = 'token_unavailable';
    public const BIND_TIMEOUT = 'bind_timeout';
    public const AUTHENTICATION_FAILED = 'authentication_failed';
    public const AUTHENTICATION_UNAVAILABLE = 'authentication_unavailable';
    public const BIND_REJECTED = 'bind_rejected';
    public const UNEXPECTED_BIND_ACK = 'unexpected_bind_ack';
    public const PENDING_OVERFLOW = 'pending_overflow';
    public const IO_ERROR = 'io_error';

    public function __construct(
        public readonly string $kind,
        string $message,
        ?\Throwable $previous = null,
    ) {
        parent::__construct($message, 0, $previous);
    }
}
