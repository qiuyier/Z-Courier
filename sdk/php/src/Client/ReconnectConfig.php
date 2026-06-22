<?php

declare(strict_types=1);

namespace ZCourier\Client;

use ZCourier\Exception\ClientException;

final readonly class ReconnectConfig
{
    public function __construct(
        public float $initialDelay = 0.25,
        public float $maxDelay = 30.0,
        public float $multiplier = 2.0,
        public float $jitter = 0.2,
        public int $maxAttempts = 0,
    ) {
        if (
            !is_finite($this->initialDelay)
            || !is_finite($this->maxDelay)
            || $this->initialDelay <= 0
            || $this->maxDelay <= 0
        ) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'reconnect delays must be finite and greater than zero',
            );
        }
        if ($this->maxDelay < $this->initialDelay) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'reconnect max delay cannot be less than initial delay',
            );
        }
        if (!is_finite($this->multiplier) || $this->multiplier < 1) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'reconnect multiplier must be at least one',
            );
        }
        if (!is_finite($this->jitter) || $this->jitter < 0 || $this->jitter > 1) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'reconnect jitter must be between zero and one',
            );
        }
        if ($this->maxAttempts < 0) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'reconnect max attempts cannot be negative',
            );
        }
    }
}
