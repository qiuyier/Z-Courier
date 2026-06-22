<?php

declare(strict_types=1);

namespace ZCourier\Client;

use Throwable;
use ZCourier\Exception\BindException;
use ZCourier\Exception\ClientException;
use ZCourier\Exception\ProtocolException;
use ZCourier\Protocol\Ack;
use ZCourier\Protocol\FrameCodec;
use ZCourier\Protocol\FrameParser;
use ZCourier\Protocol\Packet;

final class Client
{
    /** @var resource|null */
    private mixed $stream = null;
    private State $state = State::Disconnected;
    private ?Binding $binding = null;
    private ?Throwable $lastError = null;
    private FrameParser $parser;
    /** @var list<Packet> */
    private array $readQueue = [];
    /** @var list<Packet> */
    private array $pendingBeforeReady = [];
    private int $sequence = 0;

    public function __construct(private readonly Config $config)
    {
        $this->parser = new FrameParser($config->maxFramePayloadSize, $config->maxBodySize);
    }

    public function state(): State
    {
        return $this->state;
    }

    public function ready(): bool
    {
        return $this->state === State::Ready;
    }

    public function binding(): ?Binding
    {
        return $this->binding;
    }

    public function lastError(): ?Throwable
    {
        return $this->lastError;
    }

    public function connect(): Binding
    {
        if ($this->state === State::Closed || $this->state === State::Closing) {
            throw new ClientException(ClientException::CLOSED, 'client is closed');
        }
        if ($this->state === State::Ready && $this->binding !== null) {
            return $this->binding;
        }

        $this->state = State::Connecting;
        $this->binding = null;
        $this->lastError = null;
        $this->readQueue = [];
        $this->pendingBeforeReady = [];
        $this->parser->reset();

        try {
            $token = $this->resolveToken();
            $this->stream = $this->config->connector->connect(
                $this->config->address,
                $this->config->connectTimeout,
            );
            if (!is_resource($this->stream)) {
                throw new ClientException(ClientException::CONNECT_FAILED, 'connector returned an invalid stream');
            }

            $this->state = State::Binding;
            $this->setReadTimeout($this->config->bindTimeout);
            $bind = $this->newBindPacket($token);
            $this->writeAll(FrameCodec::encode($bind, $this->config->maxFramePayloadSize));
            $binding = $this->waitForBindAck($bind->messageId);
            $this->setReadTimeout(0.0);

            $this->binding = $binding;
            $this->state = State::Ready;
            $this->lastError = null;
            array_push($this->readQueue, ...$this->pendingBeforeReady);
            $this->pendingBeforeReady = [];
            return $binding;
        } catch (Throwable $error) {
            $mapped = $this->mapConnectError($error);
            $this->closeStream();
            $this->binding = null;
            $this->readQueue = [];
            $this->pendingBeforeReady = [];
            $this->lastError = $mapped;
            $this->state = State::Disconnected;
            throw $mapped;
        }
    }

    public function close(): void
    {
        if ($this->state === State::Closed) {
            return;
        }
        $this->state = State::Closing;
        $this->closeStream();
        $this->binding = null;
        $this->readQueue = [];
        $this->pendingBeforeReady = [];
        $this->parser->reset();
        $this->state = State::Closed;
    }

    private function resolveToken(): string
    {
        try {
            $token = $this->config->tokenProvider->token();
        } catch (Throwable $error) {
            throw new ClientException(
                ClientException::TOKEN_UNAVAILABLE,
                'token provider failed: ' . $error->getMessage(),
                $error,
            );
        }
        if ($token === '') {
            throw new ClientException(ClientException::TOKEN_UNAVAILABLE, 'token provider returned an empty token');
        }
        return $token;
    }

    private function newBindPacket(string $token): Packet
    {
        $messageId = 'zc-bind-' . bin2hex(random_bytes(16));
        $this->sequence++;
        return new Packet(
            msgId: Packet::MSG_ID_BIND,
            flags: Packet::FLAG_ACK_REQUIRED,
            sequence: (string) $this->sequence,
            timestamp: (string) (int) floor(microtime(true) * 1000),
            clientId: $this->config->clientId,
            deviceId: $this->config->deviceId,
            messageId: $messageId,
            traceId: $messageId,
            token: $token,
        );
    }

    private function waitForBindAck(string $bindMessageId): Binding
    {
        while (true) {
            $packet = $this->readPacket();
            if ($packet->msgId !== Packet::MSG_ID_ACK || $packet->messageId !== $bindMessageId) {
                if (count($this->pendingBeforeReady) >= $this->config->maxPendingBeforeReady) {
                    throw new ClientException(
                        ClientException::PENDING_OVERFLOW,
                        'too many packets arrived before bind completed',
                    );
                }
                $this->pendingBeforeReady[] = $packet;
                continue;
            }

            try {
                $ack = Ack::fromPacket($packet);
            } catch (ProtocolException $error) {
                throw new ClientException(
                    ClientException::UNEXPECTED_BIND_ACK,
                    'cannot decode bind ACK: ' . $error->getMessage(),
                    $error,
                );
            }
            if ($ack->messageId !== $bindMessageId || $ack->msgId !== Packet::MSG_ID_BIND) {
                throw new ClientException(
                    ClientException::UNEXPECTED_BIND_ACK,
                    'bind ACK does not match the active bind request',
                );
            }

            return match ($ack->code) {
                Ack::ACCEPTED => $this->acceptedBinding($packet),
                Ack::UNAUTHORIZED => throw new BindException(
                    ClientException::AUTHENTICATION_FAILED,
                    $ack->code,
                    $ack->reason,
                ),
                Ack::AUTH_UNAVAILABLE => throw new BindException(
                    ClientException::AUTHENTICATION_UNAVAILABLE,
                    $ack->code,
                    $ack->reason,
                ),
                Ack::DECODE_FAILED, Ack::REJECTED => throw new BindException(
                    ClientException::BIND_REJECTED,
                    $ack->code,
                    $ack->reason,
                ),
                default => throw new ClientException(
                    ClientException::UNEXPECTED_BIND_ACK,
                    "unknown bind ACK code {$ack->code}",
                ),
            };
        }
    }

    private function acceptedBinding(Packet $packet): Binding
    {
        if ($packet->sessionId === '') {
            throw new ClientException(
                ClientException::UNEXPECTED_BIND_ACK,
                'accepted bind ACK has no session ID',
            );
        }
        return new Binding($packet->clientId, $packet->deviceId, $packet->sessionId);
    }

    private function readPacket(): Packet
    {
        if ($this->readQueue !== []) {
            /** @var Packet $packet */
            $packet = array_shift($this->readQueue);
            return $packet;
        }
        if (!is_resource($this->stream)) {
            throw new ClientException(ClientException::IO_ERROR, 'connection stream is unavailable');
        }

        while (true) {
            $chunk = @fread($this->stream, $this->config->readChunkSize);
            if ($chunk === false || $chunk === '') {
                $metadata = stream_get_meta_data($this->stream);
                if (($metadata['timed_out'] ?? false) === true) {
                    throw new ClientException(ClientException::BIND_TIMEOUT, 'timed out waiting for bind ACK');
                }
                if (feof($this->stream)) {
                    throw new ClientException(ClientException::IO_ERROR, 'connection closed while reading');
                }
                continue;
            }
            $packets = $this->parser->push($chunk);
            if ($packets === []) {
                continue;
            }
            $packet = array_shift($packets);
            array_push($this->readQueue, ...$packets);
            return $packet;
        }
    }

    private function writeAll(string $data): void
    {
        if (!is_resource($this->stream)) {
            throw new ClientException(ClientException::IO_ERROR, 'connection stream is unavailable');
        }
        while ($data !== '') {
            $read = [];
            $write = [$this->stream];
            $except = [];
            [$seconds, $microseconds] = self::timeoutParts($this->config->writeTimeout);
            $selected = @stream_select($read, $write, $except, $seconds, $microseconds);
            if ($selected === false) {
                throw new ClientException(ClientException::IO_ERROR, 'cannot wait for writable connection');
            }
            if ($selected === 0) {
                throw new ClientException(ClientException::IO_ERROR, 'timed out writing to connection');
            }
            $written = @fwrite($this->stream, $data);
            if ($written === false || $written === 0) {
                throw new ClientException(ClientException::IO_ERROR, 'connection write failed');
            }
            $data = substr($data, $written);
        }
    }

    private function setReadTimeout(float $timeout): void
    {
        if (!is_resource($this->stream)) {
            throw new ClientException(ClientException::IO_ERROR, 'connection stream is unavailable');
        }
        [$seconds, $microseconds] = self::timeoutParts($timeout);
        if (!stream_set_timeout($this->stream, $seconds, $microseconds)) {
            throw new ClientException(ClientException::IO_ERROR, 'cannot configure connection timeout');
        }
    }

    private function closeStream(): void
    {
        if (is_resource($this->stream)) {
            fclose($this->stream);
        }
        $this->stream = null;
    }

    private function mapConnectError(Throwable $error): ClientException
    {
        if ($error instanceof ClientException) {
            return $error;
        }
        if ($error instanceof ProtocolException) {
            return new ClientException(
                ClientException::UNEXPECTED_BIND_ACK,
                'invalid packet while binding: ' . $error->getMessage(),
                $error,
            );
        }
        return new ClientException(
            ClientException::CONNECT_FAILED,
            'connect failed: ' . $error->getMessage(),
            $error,
        );
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
