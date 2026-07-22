<?php

declare(strict_types=1);

$autoload = dirname(__DIR__) . '/vendor/autoload.php';
if (!is_file($autoload)) {
    fwrite(STDERR, "run composer --working-dir=sdk/php install first\n");
    exit(1);
}
require $autoload;

use ZCourier\Client\CallbackTokenProvider;
use ZCourier\Client\Client;
use ZCourier\Client\Config;
use ZCourier\Client\ReconnectConfig;
use ZCourier\Client\SendRequest;
use ZCourier\Client\TlsConfig;
use ZCourier\Exception\DownlinkException;
use ZCourier\Protocol\Packet;

$client = new Client(new Config(
    address: environment('ZCOURIER_GATEWAY_ADDRESS', '127.0.0.1:8999'),
    clientId: environment('ZCOURIER_CLIENT_ID', 'example-client'),
    deviceId: environment('ZCOURIER_DEVICE_ID', 'php-worker-1'),
    tokenProvider: new CallbackTokenProvider(
        static fn (): string => environment('ZCOURIER_CLIENT_TOKEN'),
    ),
    reconnect: new ReconnectConfig(
        initialDelay: 0.5,
        maxDelay: 30.0,
        multiplier: 2.0,
        jitter: 0.2,
        maxAttempts: 0,
    ),
    tls: tlsFromEnvironment(),
));

try {
    $binding = $client->connect();
    printf(
        "connected client_id=%s device_id=%s session_id=%s\n",
        $binding->clientId,
        $binding->deviceId,
        $binding->sessionId,
    );

    $result = $client->send(new SendRequest(
        msgId: 2001,
        body: '{"source":"php-example"}',
        ackRequired: true,
    ));
    printf("upstream accepted message_id=%s code=%s\n", $result->messageId, $result->ack?->code ?? '');

    $client->run(
        handler: static function (Packet $packet): void {
            // Replace this with an idempotent transaction keyed by MessageID.
            printf(
                "downlink msg_id=%d message_id=%s body=%s\n",
                $packet->msgId,
                $packet->messageId,
                $packet->body,
            );
        },
        onError: static function (DownlinkException $error): void {
            error_log($error->getMessage());
        },
    );
} finally {
    $client->close();
}

function environment(string $name, ?string $default = null): string
{
    $value = getenv($name);
    if (is_string($value) && $value !== '') {
        return $value;
    }
    if ($default !== null) {
        return $default;
    }
    throw new RuntimeException("{$name} is required");
}

function tlsFromEnvironment(): ?TlsConfig
{
    $enabled = strtolower(optionalEnvironment('ZCOURIER_CLIENT_TLS'));
    $caFile = optionalEnvironment('ZCOURIER_CLIENT_TLS_CA_FILE');
    $serverName = optionalEnvironment('ZCOURIER_CLIENT_TLS_SERVER_NAME');
    if ($enabled === '' || in_array($enabled, ['0', 'false', 'no', 'off'], true)) {
        if ($caFile !== '' || $serverName !== '') {
            throw new RuntimeException(
                'ZCOURIER_CLIENT_TLS_CA_FILE and ZCOURIER_CLIENT_TLS_SERVER_NAME require ZCOURIER_CLIENT_TLS=1',
            );
        }
        return null;
    }
    if (!in_array($enabled, ['1', 'true', 'yes', 'on'], true)) {
        throw new RuntimeException('ZCOURIER_CLIENT_TLS must be a boolean value');
    }
    return new TlsConfig(caFile: $caFile, serverName: $serverName);
}

function optionalEnvironment(string $name): string
{
    $value = getenv($name);
    return is_string($value) ? trim($value) : '';
}
