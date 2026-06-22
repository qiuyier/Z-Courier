<?php

declare(strict_types=1);

namespace ZCourier\Client;

final readonly class StaticTokenProvider implements TokenProvider
{
    public function __construct(private string $value)
    {
    }

    public function token(): string
    {
        return $this->value;
    }
}
