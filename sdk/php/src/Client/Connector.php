<?php

declare(strict_types=1);

namespace ZCourier\Client;

interface Connector
{
    /** @return resource */
    public function connect(string $address, float $timeout): mixed;
}
