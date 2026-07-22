<?php

declare(strict_types=1);

namespace ZCourier\Client;

use ZCourier\Exception\ClientException;

final readonly class TlsConfig
{
    public string $caFile;
    public string $serverName;

    public function __construct(string $caFile = '', string $serverName = '')
    {
        $this->caFile = trim($caFile);
        $this->serverName = trim($serverName);

        if ($this->caFile !== '' && (!is_file($this->caFile) || !is_readable($this->caFile))) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                "TLS CA file {$this->caFile} is not a readable file",
            );
        }
        self::validateServerName($this->serverName);
    }

    public function serverNameForAddress(string $address): string
    {
        if ($this->serverName !== '') {
            return $this->serverName;
        }

        $target = str_contains($address, '://') ? $address : "tcp://{$address}";
        $parts = parse_url($target);
        $host = is_array($parts) && isset($parts['host'])
            ? trim($parts['host'], '[]')
            : '';
        if ($host === '') {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'TLS server name is required when address has no host',
            );
        }
        self::validateServerName($host);
        return $host;
    }

    /** @return array<string, bool|string> */
    public function streamOptions(string $address): array
    {
        $options = [
            'verify_peer' => true,
            'verify_peer_name' => true,
            'allow_self_signed' => false,
            'peer_name' => $this->serverNameForAddress($address),
            'SNI_enabled' => true,
            'disable_compression' => true,
        ];
        if ($this->caFile !== '') {
            $options['cafile'] = $this->caFile;
        }
        return $options;
    }

    private static function validateServerName(string $value): void
    {
        if ($value === '') {
            return;
        }
        if (
            str_contains($value, '://')
            || preg_match('/[\/\\\\\[\]\s]/', $value) === 1
            || (filter_var($value, FILTER_VALIDATE_IP) === false && str_contains($value, ':'))
        ) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'TLS server name must be a DNS name or IP address without a scheme, port, or path',
            );
        }
    }
}
