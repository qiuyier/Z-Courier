<?php

declare(strict_types=1);

namespace ZCourier\Client;

enum State: string
{
    case Disconnected = 'disconnected';
    case Connecting = 'connecting';
    case Binding = 'binding';
    case Ready = 'ready';
    case ReconnectWait = 'reconnect_wait';
    case Closing = 'closing';
    case Closed = 'closed';
}
