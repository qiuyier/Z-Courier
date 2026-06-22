<?php

declare(strict_types=1);

namespace ZCourier\Client;

use ZCourier\Exception\ClientException;

final class NativeConnector implements Connector
{
    public function connect(string $address, float $timeout): mixed
    {
        $target = str_contains($address, '://') ? $address : "tcp://{$address}";
        $errorCode = 0;
        $errorMessage = '';
        $stream = @stream_socket_client(
            $target,
            $errorCode,
            $errorMessage,
            $timeout,
            STREAM_CLIENT_CONNECT,
        );
        if ($stream === false) {
            throw new ClientException(
                ClientException::CONNECT_FAILED,
                "connect {$target} failed ({$errorCode}): {$errorMessage}",
            );
        }
        if (!stream_set_blocking($stream, true)) {
            fclose($stream);
            throw new ClientException(ClientException::CONNECT_FAILED, 'cannot enable blocking stream mode');
        }
        return $stream;
    }
}
