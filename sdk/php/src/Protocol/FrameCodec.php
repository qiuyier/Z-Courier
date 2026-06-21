<?php

declare(strict_types=1);

namespace ZCourier\Protocol;

use ZCourier\Exception\ProtocolException;

final class FrameCodec
{
    public const HEADER_SIZE = 8;
    public const DEFAULT_MAX_PAYLOAD_SIZE = 8 << 20;

    private function __construct()
    {
    }

    public static function encode(
        Packet $packet,
        int $maxPayloadSize = self::DEFAULT_MAX_PAYLOAD_SIZE,
    ): string {
        $payload = Codec::encode($packet);
        $payloadLength = strlen($payload);
        $limit = self::normalizeLimit($maxPayloadSize);
        if ($payloadLength > $limit) {
            throw new ProtocolException(
                ProtocolException::FRAME_TOO_LARGE,
                "frame payload has {$payloadLength} bytes; maximum is {$limit}",
            );
        }
        return pack('NN', $packet->msgId, $payloadLength) . $payload;
    }

    public static function decode(
        string $frame,
        int $maxPayloadSize = self::DEFAULT_MAX_PAYLOAD_SIZE,
        int $maxBodySize = Packet::DEFAULT_MAX_BODY_SIZE,
    ): Packet {
        $frameLength = strlen($frame);
        if ($frameLength < self::HEADER_SIZE) {
            throw new ProtocolException(
                ProtocolException::FRAME_TOO_SHORT,
                "frame has {$frameLength} bytes; expected at least " . self::HEADER_SIZE,
            );
        }

        $outerMsgId = self::headerUint32($frame, 0);
        $payloadLength = self::headerUint32($frame, 4);
        $limit = self::normalizeLimit($maxPayloadSize);
        if ($payloadLength > $limit) {
            throw new ProtocolException(
                ProtocolException::FRAME_TOO_LARGE,
                "frame payload has {$payloadLength} bytes; maximum is {$limit}",
            );
        }
        if ($payloadLength + self::HEADER_SIZE !== $frameLength) {
            throw new ProtocolException(
                ProtocolException::FRAME_LENGTH_MISMATCH,
                "frame length is {$frameLength}; expected " . ($payloadLength + self::HEADER_SIZE),
            );
        }

        return self::decodePayload($outerMsgId, substr($frame, self::HEADER_SIZE), $maxBodySize);
    }

    public static function decodePayload(int $outerMsgId, string $payload, int $maxBodySize): Packet
    {
        $bodyLimit = $maxBodySize === 0 ? Packet::DEFAULT_MAX_BODY_SIZE : $maxBodySize;
        $packet = Codec::decode($payload, $bodyLimit);
        if ($outerMsgId !== $packet->msgId) {
            throw new ProtocolException(
                ProtocolException::OUTER_INNER_MSG_ID_MISMATCH,
                "outer MsgID {$outerMsgId} differs from inner MsgID {$packet->msgId}",
            );
        }
        return $packet;
    }

    public static function payloadLength(string $header): int
    {
        if (strlen($header) < self::HEADER_SIZE) {
            throw new ProtocolException(ProtocolException::FRAME_TOO_SHORT, 'frame header is incomplete');
        }
        return self::headerUint32($header, 4);
    }

    private static function headerUint32(string $data, int $offset): int
    {
        /** @var array{value: int} $decoded */
        $decoded = unpack('Nvalue', $data, $offset);
        return $decoded['value'];
    }

    private static function normalizeLimit(int $limit): int
    {
        if ($limit < 0) {
            throw new ProtocolException(ProtocolException::FRAME_TOO_LARGE, 'maximum frame payload cannot be negative');
        }
        return $limit === 0 ? self::DEFAULT_MAX_PAYLOAD_SIZE : $limit;
    }
}
