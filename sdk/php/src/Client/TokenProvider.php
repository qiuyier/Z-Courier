<?php

declare(strict_types=1);

namespace ZCourier\Client;

interface TokenProvider
{
    public function token(): string;
}
