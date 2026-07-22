<?php

declare(strict_types=1);

namespace ZCourier\Client;

use ZCourier\Exception\ClientException;

/** @internal */
final class TlsConnector implements Connector
{
    public function __construct(
        private readonly Connector $next,
        private readonly TlsConfig $config,
    ) {
    }

    public function connect(string $address, float $timeout): mixed
    {
        $startedAt = hrtime(true);
        $stream = $this->next->connect($address, $timeout);
        if (!is_resource($stream)) {
            throw new ClientException(ClientException::CONNECT_FAILED, 'connector returned an invalid stream');
        }

        try {
            if (!stream_set_blocking($stream, true)) {
                throw new ClientException(ClientException::CONNECT_FAILED, 'cannot enable blocking stream mode');
            }
            foreach ($this->config->streamOptions($address) as $name => $value) {
                if (!stream_context_set_option($stream, 'ssl', $name, $value)) {
                    throw new ClientException(
                        ClientException::CONNECT_FAILED,
                        "cannot configure TLS option {$name}",
                    );
                }
            }

            $remaining = $timeout - ((hrtime(true) - $startedAt) / 1_000_000_000);
            if ($remaining <= 0) {
                throw new ClientException(ClientException::CONNECT_FAILED, 'TLS handshake timed out');
            }
            [$seconds, $microseconds] = self::timeoutParts($remaining);
            if (!stream_set_timeout($stream, $seconds, $microseconds)) {
                throw new ClientException(ClientException::CONNECT_FAILED, 'cannot configure TLS handshake timeout');
            }

            error_clear_last();
            $enabled = @stream_socket_enable_crypto($stream, true, self::cryptoMethod());
            $lastError = error_get_last();
            if ($enabled !== true) {
                $metadata = stream_get_meta_data($stream);
                $message = $metadata['timed_out'] === true
                    ? 'TLS handshake timed out'
                    : 'TLS handshake failed';
                if ($lastError !== null) {
                    $detail = preg_replace('/^stream_socket_enable_crypto\(\):\s*/', '', $lastError['message']);
                    if (is_string($detail) && $detail !== '') {
                        $message .= ': ' . $detail;
                    }
                }
                throw new ClientException(ClientException::CONNECT_FAILED, $message);
            }
            return $stream;
        } catch (\Throwable $error) {
            fclose($stream);
            throw $error;
        }
    }

    private static function cryptoMethod(): int
    {
        return STREAM_CRYPTO_METHOD_TLSv1_2_CLIENT | STREAM_CRYPTO_METHOD_TLSv1_3_CLIENT;
    }

    /** @return array{int, int} */
    private static function timeoutParts(float $timeout): array
    {
        $seconds = (int) floor($timeout);
        $microseconds = (int) round(($timeout - $seconds) * 1_000_000);
        if ($microseconds >= 1_000_000) {
            $seconds++;
            $microseconds = 0;
        }
        return [$seconds, $microseconds];
    }
}
