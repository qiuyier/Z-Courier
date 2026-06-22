<?php

declare(strict_types=1);

namespace ZCourier\Client;

use ZCourier\Exception\ClientException;
use ZCourier\Protocol\FrameCodec;
use ZCourier\Protocol\Packet;

final readonly class Config
{
    public TokenProvider $tokenProvider;
    public Connector $connector;

    public function __construct(
        public string $address,
        public string $clientId,
        public string $deviceId,
        string $token = '',
        ?TokenProvider $tokenProvider = null,
        ?Connector $connector = null,
        public float $connectTimeout = 5.0,
        public float $bindTimeout = 5.0,
        public float $writeTimeout = 5.0,
        public float $ackTimeout = 5.0,
        public int $maxFramePayloadSize = FrameCodec::DEFAULT_MAX_PAYLOAD_SIZE,
        public int $maxBodySize = Packet::DEFAULT_MAX_BODY_SIZE,
        public int $maxPendingBeforeReady = 128,
        public int $inboundBuffer = 128,
        public int $readChunkSize = 8192,
        public int $downlinkDedupCapacity = 10000,
    ) {
        if (trim($address) === '' || trim($clientId) === '' || trim($deviceId) === '') {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'address, client ID, and device ID are required',
            );
        }
        if (($token === '') === ($tokenProvider === null)) {
            throw new ClientException(
                ClientException::INVALID_CONFIG,
                'configure exactly one of token or token provider',
            );
        }
        if ($connectTimeout <= 0 || $bindTimeout <= 0 || $writeTimeout <= 0 || $ackTimeout <= 0) {
            throw new ClientException(ClientException::INVALID_CONFIG, 'timeouts must be greater than zero');
        }
        if (
            $maxFramePayloadSize <= 0
            || $maxBodySize < 0
            || $maxPendingBeforeReady <= 0
            || $inboundBuffer <= 0
            || $downlinkDedupCapacity <= 0
            || $readChunkSize <= 0
        ) {
            throw new ClientException(ClientException::INVALID_CONFIG, 'buffer and size limits are invalid');
        }

        $this->tokenProvider = $tokenProvider ?? new StaticTokenProvider($token);
        $this->connector = $connector ?? new NativeConnector();
    }
}
