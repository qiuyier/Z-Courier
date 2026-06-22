<?php

declare(strict_types=1);

namespace ZCourier\Client;

use Throwable;
use ZCourier\Exception\AckException;
use ZCourier\Exception\BindException;
use ZCourier\Exception\ClientException;
use ZCourier\Exception\DownlinkException;
use ZCourier\Exception\ProtocolException;
use ZCourier\Protocol\Ack;
use ZCourier\Protocol\DeliveryAck;
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
    private array $wireQueue = [];
    /** @var list<Packet> */
    private array $inboundQueue = [];
    /** @var list<Packet> */
    private array $pendingBeforeReady = [];
    private int $sequence = 0;
    private string $token = '';
    private MessageDeduper $deduper;

    public function __construct(private readonly Config $config)
    {
        $this->parser = new FrameParser($config->maxFramePayloadSize, $config->maxBodySize);
        $this->deduper = new MessageDeduper($config->downlinkDedupCapacity);
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
        $this->wireQueue = [];
        $this->inboundQueue = [];
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
            $this->token = $token;
            $this->state = State::Ready;
            $this->lastError = null;
            foreach ($this->pendingBeforeReady as $packet) {
                $this->enqueueInbound($packet);
            }
            $this->pendingBeforeReady = [];
            return $binding;
        } catch (Throwable $error) {
            $mapped = $this->mapConnectError($error);
            $this->closeStream();
            $this->binding = null;
            $this->token = '';
            $this->wireQueue = [];
            $this->inboundQueue = [];
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
        $this->token = '';
        $this->wireQueue = [];
        $this->inboundQueue = [];
        $this->pendingBeforeReady = [];
        $this->parser->reset();
        $this->state = State::Closed;
    }

    public function send(SendRequest $request): SendResult
    {
        if ($this->state === State::Closed || $this->state === State::Closing) {
            throw new ClientException(ClientException::CLOSED, 'client is closed');
        }
        if ($this->state !== State::Ready || $this->binding === null || !is_resource($this->stream)) {
            throw new ClientException(ClientException::NOT_READY, 'client is not ready');
        }
        if ($request->msgId < 0 || $request->msgId > 0xFFFFFFFF || $request->flags < 0 || $request->flags > 0xFFFF) {
            throw new ClientException(ClientException::INVALID_REQUEST, 'MsgID or flags are out of range');
        }
        if (Packet::isReservedMsgId($request->msgId)) {
            throw new ClientException(
                ClientException::RESERVED_MSG_ID,
                "MsgID {$request->msgId} is reserved by the protocol",
            );
        }

        $messageId = $request->messageId !== ''
            ? $request->messageId
            : 'zc-msg-' . bin2hex(random_bytes(16));
        $traceId = $request->traceId !== '' ? $request->traceId : $messageId;
        $ackRequired = $request->ackRequired || ($request->flags & Packet::FLAG_ACK_REQUIRED) !== 0;
        $flags = $ackRequired
            ? $request->flags | Packet::FLAG_ACK_REQUIRED
            : $request->flags;
        $this->sequence++;
        $packet = new Packet(
            msgId: $request->msgId,
            body: $request->body,
            flags: $flags,
            sequence: (string) $this->sequence,
            timestamp: (string) (int) floor(microtime(true) * 1000),
            clientId: $this->binding->clientId,
            deviceId: $this->binding->deviceId,
            sessionId: $this->binding->sessionId,
            messageId: $messageId,
            traceId: $traceId,
            token: $this->token,
        );

        try {
            $this->writeAll(FrameCodec::encode($packet, $this->config->maxFramePayloadSize));
            if (!$ackRequired) {
                return new SendResult($messageId);
            }
            $this->setReadTimeout($this->config->ackTimeout);
            try {
                $ack = $this->waitForBusinessAck($request->msgId, $messageId);
            } finally {
                $this->setReadTimeout(0.0);
            }
            return new SendResult($messageId, $ack);
        } catch (ProtocolException $error) {
            $mapped = new ClientException(
                ClientException::IO_ERROR,
                'invalid frame while waiting for ACK: ' . $error->getMessage(),
                $error,
            );
            $this->disconnect($mapped);
            throw $mapped;
        } catch (ClientException $error) {
            if (in_array($error->kind, [ClientException::IO_ERROR, ClientException::INBOUND_OVERFLOW], true)) {
                $this->disconnect($error);
            }
            throw $error;
        }
    }

    public function receive(?float $timeout = null): Packet
    {
        $this->ensureReady();
        if ($timeout !== null && $timeout <= 0) {
            throw new ClientException(ClientException::INVALID_REQUEST, 'receive timeout must be greater than zero');
        }

        try {
            $this->setReadTimeout($timeout ?? 0.0);
            while (true) {
                if ($this->inboundQueue !== []) {
                    /** @var Packet $packet */
                    $packet = array_shift($this->inboundQueue);
                } else {
                    $packet = $this->readPacket(
                        ClientException::RECEIVE_TIMEOUT,
                        'timed out waiting for a downlink packet',
                    );
                }
                if ($packet->msgId !== Packet::MSG_ID_ACK) {
                    return $packet;
                }
            }
        } catch (ProtocolException $error) {
            $mapped = new ClientException(
                ClientException::IO_ERROR,
                'invalid frame while receiving a downlink: ' . $error->getMessage(),
                $error,
            );
            $this->disconnect($mapped);
            throw $mapped;
        } catch (ClientException $error) {
            if (in_array($error->kind, [ClientException::IO_ERROR, ClientException::INBOUND_OVERFLOW], true)) {
                $this->disconnect($error);
            }
            throw $error;
        } finally {
            if (is_resource($this->stream)) {
                $this->setReadTimeout(0.0);
            }
        }
    }

    public function acknowledgeDownlink(Packet $packet): Ack
    {
        $this->ensureReady();
        $target = $this->downlinkTarget($packet);
        $this->deduper->mark($target['messageId']);

        $messageId = 'zc-dack-' . bin2hex(random_bytes(16));
        $this->sequence++;
        $deliveryAck = new Packet(
            msgId: Packet::MSG_ID_DOWNLINK_ACK,
            body: (new DeliveryAck($target['messageId']))->toJson(),
            flags: Packet::FLAG_ACK_REQUIRED,
            sequence: (string) $this->sequence,
            timestamp: (string) (int) floor(microtime(true) * 1000),
            clientId: $this->binding->clientId,
            deviceId: $this->binding->deviceId,
            sessionId: $this->binding->sessionId,
            messageId: $messageId,
            traceId: $target['traceId'] !== '' ? $target['traceId'] : $target['messageId'],
            token: $this->token,
        );

        try {
            $this->writeAll(FrameCodec::encode($deliveryAck, $this->config->maxFramePayloadSize));
            $this->setReadTimeout($this->config->ackTimeout);
            try {
                return $this->waitForBusinessAck(Packet::MSG_ID_DOWNLINK_ACK, $messageId);
            } finally {
                if (is_resource($this->stream)) {
                    $this->setReadTimeout(0.0);
                }
            }
        } catch (ProtocolException $error) {
            $mapped = new ClientException(
                ClientException::IO_ERROR,
                'invalid frame while waiting for delivery ACK acceptance: ' . $error->getMessage(),
                $error,
            );
            $this->disconnect($mapped);
            throw $mapped;
        } catch (ClientException $error) {
            if (in_array($error->kind, [ClientException::IO_ERROR, ClientException::INBOUND_OVERFLOW], true)) {
                $this->disconnect($error);
            }
            throw $error;
        }
    }

    /**
     * @param callable(Packet): mixed $handler
     * @param null|callable(DownlinkException): mixed $onError
     */
    public function run(
        callable $handler,
        bool $manualAck = false,
        ?callable $onError = null,
        ?int $maxMessages = null,
    ): void {
        if ($maxMessages !== null && $maxMessages <= 0) {
            throw new ClientException(ClientException::INVALID_REQUEST, 'max messages must be greater than zero');
        }

        $received = 0;
        while ($maxMessages === null || $received < $maxMessages) {
            $packet = $this->receive();
            $received++;
            $target = $this->tryDownlinkTarget($packet);
            if ($target !== null && $this->deduper->contains($target['messageId'])) {
                try {
                    $this->acknowledgeDownlink($packet);
                } catch (Throwable $error) {
                    $this->reportDownlinkError(
                        $onError,
                        ClientException::AUTO_ACK_FAILED,
                        'ack_duplicate',
                        $packet,
                        $error,
                    );
                }
                continue;
            }

            try {
                $handler($packet);
            } catch (Throwable $error) {
                $this->reportDownlinkError(
                    $onError,
                    ClientException::HANDLER_FAILED,
                    'handle',
                    $packet,
                    $error,
                );
                continue;
            }
            if ($target === null || $manualAck) {
                continue;
            }

            try {
                $this->acknowledgeDownlink($packet);
            } catch (Throwable $error) {
                $this->reportDownlinkError(
                    $onError,
                    ClientException::AUTO_ACK_FAILED,
                    'ack',
                    $packet,
                    $error,
                );
            }
        }
    }

    private function ensureReady(): void
    {
        if ($this->state === State::Closed || $this->state === State::Closing) {
            throw new ClientException(ClientException::CLOSED, 'client is closed');
        }
        if ($this->state !== State::Ready || $this->binding === null || !is_resource($this->stream)) {
            throw new ClientException(ClientException::NOT_READY, 'client is not ready');
        }
    }

    /** @return array{msgId: int, messageId: string, traceId: string} */
    private function downlinkTarget(Packet $packet): array
    {
        if (
            Packet::isReservedMsgId($packet->msgId)
            || ($packet->flags & Packet::FLAG_ACK_REQUIRED) === 0
            || $packet->messageId === ''
        ) {
            throw new ClientException(
                ClientException::INVALID_DOWNLINK,
                'downlink must use a business MsgID, require an ACK, and include a message ID',
            );
        }
        return [
            'msgId' => $packet->msgId,
            'messageId' => $packet->messageId,
            'traceId' => $packet->traceId,
        ];
    }

    /** @return null|array{msgId: int, messageId: string, traceId: string} */
    private function tryDownlinkTarget(Packet $packet): ?array
    {
        try {
            return $this->downlinkTarget($packet);
        } catch (ClientException) {
            return null;
        }
    }

    /** @param null|callable(DownlinkException): mixed $onError */
    private function reportDownlinkError(
        ?callable $onError,
        string $kind,
        string $operation,
        Packet $packet,
        Throwable $error,
    ): void {
        if ($onError === null) {
            return;
        }
        $downlinkError = new DownlinkException(
            $kind,
            $operation,
            $packet->msgId,
            $packet->messageId,
            sprintf(
                'downlink %s failed: msg_id=%d message_id=%s: %s',
                $operation,
                $packet->msgId,
                $packet->messageId,
                $error->getMessage(),
            ),
            $error,
        );
        try {
            $onError($downlinkError);
        } catch (Throwable) {
            // Error callbacks must not stop the receive loop.
        }
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
            $packet = $this->readPacket(ClientException::BIND_TIMEOUT, 'timed out waiting for bind ACK');
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

    private function waitForBusinessAck(int $msgId, string $messageId): Ack
    {
        while (true) {
            $packet = $this->readPacket(ClientException::ACK_TIMEOUT, 'timed out waiting for message ACK');
            if ($packet->msgId !== Packet::MSG_ID_ACK || $packet->messageId !== $messageId) {
                if ($packet->msgId !== Packet::MSG_ID_ACK) {
                    $this->enqueueInbound($packet);
                }
                continue;
            }

            try {
                $ack = Ack::fromPacket($packet);
            } catch (ProtocolException $error) {
                throw new ClientException(
                    ClientException::UNEXPECTED_ACK,
                    'cannot decode message ACK: ' . $error->getMessage(),
                    $error,
                );
            }
            if ($ack->messageId !== $messageId || $ack->msgId !== $msgId) {
                throw new ClientException(
                    ClientException::UNEXPECTED_ACK,
                    'message ACK does not match the sent request',
                );
            }
            if ($ack->code === Ack::ACCEPTED) {
                return $ack;
            }
            $kind = match ($ack->code) {
                Ack::UNAUTHORIZED => ClientException::AUTHENTICATION_FAILED,
                Ack::AUTH_UNAVAILABLE => ClientException::AUTHENTICATION_UNAVAILABLE,
                default => ClientException::ACK_REJECTED,
            };
            throw new AckException($kind, $ack);
        }
    }

    private function enqueueInbound(Packet $packet): void
    {
        if (count($this->inboundQueue) >= $this->config->inboundBuffer) {
            throw new ClientException(
                ClientException::INBOUND_OVERFLOW,
                'inbound packet buffer is full',
            );
        }
        $this->inboundQueue[] = $packet;
    }

    private function readPacket(string $timeoutKind, string $timeoutMessage): Packet
    {
        if ($this->wireQueue !== []) {
            /** @var Packet $packet */
            $packet = array_shift($this->wireQueue);
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
                    throw new ClientException($timeoutKind, $timeoutMessage);
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
            array_push($this->wireQueue, ...$packets);
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

    private function disconnect(ClientException $error): void
    {
        $this->closeStream();
        $this->binding = null;
        $this->token = '';
        $this->wireQueue = [];
        $this->inboundQueue = [];
        $this->pendingBeforeReady = [];
        $this->parser->reset();
        $this->lastError = $error;
        $this->state = State::Disconnected;
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
