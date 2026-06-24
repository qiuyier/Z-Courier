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
            $errorCodeText = is_int($errorCode) ? (string) $errorCode : '0';
            $errorMessageText = is_string($errorMessage) ? $errorMessage : '';
            throw new ClientException(
                ClientException::CONNECT_FAILED,
                "connect {$target} failed ({$errorCodeText}): {$errorMessageText}",
            );
        }
        if (!stream_set_blocking($stream, true)) {
            fclose($stream);
            throw new ClientException(ClientException::CONNECT_FAILED, 'cannot enable blocking stream mode');
        }
        return $stream;
    }
}
