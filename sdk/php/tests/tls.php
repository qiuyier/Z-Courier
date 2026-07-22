<?php

declare(strict_types=1);

use ZCourier\Client\CallbackTokenProvider;
use ZCourier\Client\Client as TlsGatewayClient;
use ZCourier\Client\Config as TlsClientConfig;
use ZCourier\Client\Connector as TlsConnectorContract;
use ZCourier\Client\NativeConnector as TlsNativeConnector;
use ZCourier\Client\ReconnectConfig as TlsReconnectConfig;
use ZCourier\Client\TlsConfig;
use ZCourier\Exception\ClientException as TlsClientException;
use ZCourier\Protocol\Packet as TlsPacket;

final class PhpTlsTrackingConnector implements TlsConnectorContract
{
    public int $calls = 0;
    private TlsNativeConnector $next;

    public function __construct()
    {
        $this->next = new TlsNativeConnector();
    }

    public function connect(string $address, float $timeout): mixed
    {
        $this->calls++;
        return $this->next->connect($address, $timeout);
    }
}

(static function (): void {
    echo "testing PHP TLS configuration...\n";
    assertSame(
        'gateway.example.internal',
        (new TlsConfig())->serverNameForAddress('gateway.example.internal:8999'),
        'TLS derives server name from address',
    );
    assertSame(
        '::1',
        (new TlsConfig())->serverNameForAddress('[::1]:8999'),
        'TLS derives IPv6 server name from address',
    );
    assertSame(
        false,
        array_key_exists('cafile', (new TlsConfig())->streamOptions('gateway.example.internal:8999')),
        'TLS uses system roots when no CA file is configured',
    );
    assertClientError(
        TlsClientException::INVALID_CONFIG,
        static fn () => new TlsConfig(serverName: 'https://gateway.example.internal'),
        'TLS rejects server name scheme',
    );
    assertClientError(
        TlsClientException::INVALID_CONFIG,
        static fn () => new TlsConfig(caFile: '/path/that/does/not/exist/ca.crt'),
        'TLS rejects unreadable CA file',
    );

    $trusted = phpTlsCreatePki('trusted');
    $untrusted = phpTlsCreatePki('untrusted');
    try {
        echo "testing PHP TLS accepted bind...\n";
        [$serverPid, $address] = startPhpTlsBindServer($trusted, 'accepted');
        $connector = new PhpTlsTrackingConnector();
        $client = new TlsGatewayClient(new TlsClientConfig(
            address: $address,
            clientId: 'claimed-client',
            deviceId: 'device-1',
            token: 'token-1',
            connector: $connector,
            tls: new TlsConfig(caFile: $trusted['caFile']),
        ));
        $binding = $client->connect();
        assertSame('php-session-1', $binding->sessionId, 'TLS bind session ID');
        assertSame(true, $client->ready(), 'TLS client readiness');
        assertSame(1, $connector->calls, 'TLS preserves injected connector');
        $client->close();
        finishBindServer($serverPid, 'TLS accepted bind server');

        echo "testing PHP TLS unknown CA rejection...\n";
        [$serverPid, $address] = startPhpTlsBindServer($trusted, 'handshake_rejected');
        $client = new TlsGatewayClient(new TlsClientConfig(
            address: $address,
            clientId: 'claimed-client',
            deviceId: 'device-1',
            token: 'token-1',
            tls: new TlsConfig(caFile: $untrusted['caFile']),
        ));
        $error = assertClientError(
            TlsClientException::CONNECT_FAILED,
            static fn () => $client->connect(),
            'TLS rejects unknown CA',
        );
        assertSame(true, str_contains($error->getMessage(), 'TLS handshake failed'), 'unknown CA TLS error');
        $client->close();
        finishBindServer($serverPid, 'TLS unknown CA server');

        echo "testing PHP TLS server-name rejection...\n";
        [$serverPid, $address] = startPhpTlsBindServer($trusted, 'handshake_rejected');
        $client = new TlsGatewayClient(new TlsClientConfig(
            address: $address,
            clientId: 'claimed-client',
            deviceId: 'device-1',
            token: 'token-1',
            tls: new TlsConfig(
                caFile: $trusted['caFile'],
                serverName: 'wrong.gateway.example.internal',
            ),
        ));
        assertClientError(
            TlsClientException::CONNECT_FAILED,
            static fn () => $client->connect(),
            'TLS rejects mismatched server name',
        );
        $client->close();
        finishBindServer($serverPid, 'TLS server-name mismatch server');

        echo "testing PHP TLS handshake timeout...\n";
        [$serverPid, $address] = startPhpTlsBindServer($trusted, 'handshake_timeout');
        $client = new TlsGatewayClient(new TlsClientConfig(
            address: $address,
            clientId: 'claimed-client',
            deviceId: 'device-1',
            token: 'token-1',
            connectTimeout: 0.05,
            tls: new TlsConfig(caFile: $trusted['caFile']),
        ));
        $error = assertClientError(
            TlsClientException::CONNECT_FAILED,
            static fn () => $client->connect(),
            'TLS handshake timeout',
        );
        assertSame(true, str_contains($error->getMessage(), 'timed out'), 'TLS timeout error detail');
        $client->close();
        finishBindServer($serverPid, 'TLS handshake timeout server');

        echo "testing PHP TLS reconnect...\n";
        [$serverPid, $address] = startPhpTlsReconnectServer($trusted);
        $connector = new PhpTlsTrackingConnector();
        $tokenCalls = 0;
        $client = new TlsGatewayClient(new TlsClientConfig(
            address: $address,
            clientId: 'claimed-client',
            deviceId: 'device-1',
            tokenProvider: new CallbackTokenProvider(static function () use (&$tokenCalls): string {
                $tokenCalls++;
                return "token-{$tokenCalls}";
            }),
            connector: $connector,
            reconnect: new TlsReconnectConfig(
                initialDelay: 0.001,
                maxDelay: 0.001,
                jitter: 0.0,
                maxAttempts: 2,
            ),
            tls: new TlsConfig(caFile: $trusted['caFile']),
        ));
        $firstBinding = $client->connect();
        assertSame('php-reconnect-session-1', $firstBinding->sessionId, 'initial TLS reconnect session');
        $messages = [];
        $client->run(
            static function (TlsPacket $packet) use (&$messages): void {
                $messages[] = $packet->messageId;
            },
            maxMessages: 2,
        );
        assertSame(['reconnect-1', 'reconnect-2'], $messages, 'downlinks across TLS reconnect');
        assertSame('php-reconnect-session-2', $client->binding()?->sessionId, 'binding after TLS reconnect');
        assertSame(2, $tokenCalls, 'token provider calls across TLS reconnect');
        assertSame(2, $connector->calls, 'raw connector calls across TLS reconnect');
        $client->close();
        finishBindServer($serverPid, 'TLS reconnect server');
    } finally {
        phpTlsRemovePki($trusted);
        phpTlsRemovePki($untrusted);
    }
})();

/**
 * @return array{directory: string, caFile: string, certFile: string, keyFile: string}
 */
function phpTlsCreatePki(string $name): array
{
    if (!extension_loaded('openssl')) {
        throw new RuntimeException('PHP TLS tests require the openssl extension');
    }
    $directory = sys_get_temp_dir() . '/z-courier-php-tls-' . $name . '-' . bin2hex(random_bytes(8));
    if (!mkdir($directory, 0700, true) && !is_dir($directory)) {
        throw new RuntimeException("cannot create TLS test directory {$directory}");
    }
    $configFile = $directory . '/openssl.cnf';
    $config = <<<'OPENSSL'
[ req ]
distinguished_name = req_dn
prompt = no
req_extensions = v3_req

[ req_dn ]
CN = socket-pair

[ v3_req ]
subjectAltName = @alt_names

[ v3_ca ]
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
basicConstraints = critical, CA:true
keyUsage = critical, keyCertSign, cRLSign

[ v3_server ]
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid,issuer
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = socket-pair
DNS.2 = socket-sequence
DNS.3 = localhost
IP.1 = 127.0.0.1
OPENSSL;
    phpTlsWriteFile($configFile, $config, 0600);

    $keyOptions = [
        'config' => $configFile,
        'digest_alg' => 'sha256',
        'private_key_bits' => 2048,
        'private_key_type' => OPENSSL_KEYTYPE_RSA,
    ];
    $caKey = openssl_pkey_new($keyOptions);
    if ($caKey === false) {
        throw new RuntimeException('cannot create TLS test CA key');
    }
    $caCsr = openssl_csr_new(['commonName' => "Z-Courier PHP {$name} CA"], $caKey, $keyOptions);
    if ($caCsr === false) {
        throw new RuntimeException('cannot create TLS test CA CSR');
    }
    $caCertificate = openssl_csr_sign(
        $caCsr,
        null,
        $caKey,
        1,
        ['config' => $configFile, 'digest_alg' => 'sha256', 'x509_extensions' => 'v3_ca'],
        random_int(1, PHP_INT_MAX),
    );
    if ($caCertificate === false || !openssl_x509_export($caCertificate, $caPem)) {
        throw new RuntimeException('cannot create TLS test CA certificate');
    }

    $serverKey = openssl_pkey_new($keyOptions);
    if ($serverKey === false) {
        throw new RuntimeException('cannot create TLS test server key');
    }
    $serverCsr = openssl_csr_new(
        ['commonName' => 'socket-pair'],
        $serverKey,
        $keyOptions,
    );
    if ($serverCsr === false) {
        throw new RuntimeException('cannot create TLS test server CSR');
    }
    $serverCertificate = openssl_csr_sign(
        $serverCsr,
        $caCertificate,
        $caKey,
        1,
        ['config' => $configFile, 'digest_alg' => 'sha256', 'x509_extensions' => 'v3_server'],
        random_int(1, PHP_INT_MAX),
    );
    if ($serverCertificate === false || !openssl_x509_export($serverCertificate, $serverPem)) {
        throw new RuntimeException('cannot create TLS test server certificate');
    }
    if (!openssl_pkey_export($serverKey, $serverKeyPem, null, $keyOptions)) {
        throw new RuntimeException('cannot export TLS test server key');
    }

    $caFile = $directory . '/ca.crt';
    $certFile = $directory . '/server.crt';
    $keyFile = $directory . '/server.key';
    phpTlsWriteFile($caFile, $caPem, 0600);
    phpTlsWriteFile($certFile, $serverPem, 0600);
    phpTlsWriteFile($keyFile, $serverKeyPem, 0600);
    return compact('directory', 'caFile', 'certFile', 'keyFile');
}

/** @param array{directory: string, caFile: string, certFile: string, keyFile: string} $pki */
function phpTlsRemovePki(array $pki): void
{
    foreach ([$pki['caFile'], $pki['certFile'], $pki['keyFile'], $pki['directory'] . '/openssl.cnf'] as $file) {
        if (is_file($file)) {
            unlink($file);
        }
    }
    if (is_dir($pki['directory'])) {
        rmdir($pki['directory']);
    }
}

function phpTlsWriteFile(string $path, string $contents, int $permissions): void
{
    if (file_put_contents($path, $contents) === false || !chmod($path, $permissions)) {
        throw new RuntimeException("cannot write TLS test file {$path}");
    }
}

/**
 * @param array{directory: string, caFile: string, certFile: string, keyFile: string} $pki
 * @return array{int, string}
 */
function startPhpTlsBindServer(array $pki, string $mode): array
{
    [$server, $address] = phpTlsListen();
    $pid = pcntl_fork();
    if ($pid === -1) {
        throw new RuntimeException("cannot fork {$mode} TLS server");
    }
    if ($pid === 0) {
        try {
            $connection = @stream_socket_accept($server, 5);
            fclose($server);
            if ($connection === false) {
                throw new RuntimeException('cannot accept TLS test connection');
            }
            if ($mode === 'handshake_timeout') {
                usleep(250_000);
                fclose($connection);
                exit(0);
            }
            $enabled = phpTlsEnableServer($connection, $pki);
            if ($mode === 'handshake_rejected') {
                if ($enabled === true) {
                    waitForClientClose($connection);
                }
                fclose($connection);
                exit(0);
            }
            if ($enabled !== true) {
                throw new RuntimeException('TLS server handshake failed');
            }
            serveBind($connection, $mode);
            fclose($connection);
            exit(0);
        } catch (Throwable $error) {
            fwrite(STDERR, "{$mode} TLS server: {$error->getMessage()}\n");
            exit(1);
        }
    }
    fclose($server);
    return [$pid, $address];
}

/**
 * @param array{directory: string, caFile: string, certFile: string, keyFile: string} $pki
 * @return array{int, string}
 */
function startPhpTlsReconnectServer(array $pki): array
{
    [$server, $address] = phpTlsListen();
    $pid = pcntl_fork();
    if ($pid === -1) {
        throw new RuntimeException('cannot fork TLS reconnect server');
    }
    if ($pid === 0) {
        try {
            $first = @stream_socket_accept($server, 5);
            if ($first === false || phpTlsEnableServer($first, $pki) !== true) {
                throw new RuntimeException('first TLS reconnect handshake failed');
            }
            $firstParser = acceptReconnectBind($first, 'token-1', 'php-reconnect-session-1');
            $firstDownlink = testDownlink('reconnect-1', true, 'php-reconnect-session-1');
            writeServerPacket($first, $firstDownlink);
            readAndRespondToDeliveryAck($first, $firstParser, $firstDownlink, token: 'token-1');
            fclose($first);

            $second = @stream_socket_accept($server, 5);
            fclose($server);
            if ($second === false || phpTlsEnableServer($second, $pki) !== true) {
                throw new RuntimeException('second TLS reconnect handshake failed');
            }
            $secondParser = acceptReconnectBind($second, 'token-2', 'php-reconnect-session-2');
            $secondDownlink = testDownlink('reconnect-2', true, 'php-reconnect-session-2');
            writeServerPacket($second, $secondDownlink);
            readAndRespondToDeliveryAck($second, $secondParser, $secondDownlink, token: 'token-2');
            waitForClientClose($second);
            fclose($second);
            exit(0);
        } catch (Throwable $error) {
            fwrite(STDERR, "TLS reconnect server: {$error->getMessage()}\n");
            exit(1);
        }
    }
    fclose($server);
    return [$pid, $address];
}

/** @return array{mixed, string} */
function phpTlsListen(): array
{
    $errorCode = 0;
    $errorMessage = '';
    $server = @stream_socket_server(
        'tcp://127.0.0.1:0',
        $errorCode,
        $errorMessage,
        STREAM_SERVER_BIND | STREAM_SERVER_LISTEN,
    );
    if ($server === false) {
        throw new RuntimeException("cannot listen for PHP TLS test ({$errorCode}): {$errorMessage}");
    }
    $address = stream_socket_get_name($server, false);
    if (!is_string($address) || $address === '') {
        fclose($server);
        throw new RuntimeException('cannot read PHP TLS test listener address');
    }
    return [$server, $address];
}

/**
 * @param resource $stream
 * @param array{directory: string, caFile: string, certFile: string, keyFile: string} $pki
 */
function phpTlsEnableServer(mixed $stream, array $pki): bool
{
    if (!stream_set_blocking($stream, true) || !stream_set_timeout($stream, 5)) {
        throw new RuntimeException('cannot configure TLS server stream');
    }
    foreach ([
        'local_cert' => $pki['certFile'],
        'local_pk' => $pki['keyFile'],
        'verify_peer' => false,
        'verify_peer_name' => false,
        'allow_self_signed' => false,
        'disable_compression' => true,
    ] as $name => $value) {
        if (!stream_context_set_option($stream, 'ssl', $name, $value)) {
            throw new RuntimeException("cannot configure TLS server option {$name}");
        }
    }
    $enabled = @stream_socket_enable_crypto(
        $stream,
        true,
        STREAM_CRYPTO_METHOD_TLSv1_2_SERVER | STREAM_CRYPTO_METHOD_TLSv1_3_SERVER,
    );
    return $enabled === true;
}
