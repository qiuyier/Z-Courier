<?php

declare(strict_types=1);

namespace ZCourier\Client;

use Closure;

final readonly class CallbackTokenProvider implements TokenProvider
{
    private Closure $callback;

    public function __construct(callable $callback)
    {
        $this->callback = Closure::fromCallable($callback);
    }

    public function token(): string
    {
        $token = ($this->callback)();
        return is_string($token) ? $token : '';
    }
}
