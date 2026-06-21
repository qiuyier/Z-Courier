<?php

declare(strict_types=1);

namespace ZCourier\Protocol;

use ZCourier\Exception\ProtocolException;

final class Codec
{
    private function __construct()
    {
    }

    public static function encode(Packet $packet): string
    {
        self::requireUnsigned($packet->flags, 0xFFFF, 'flags');
        self::requireUnsigned($packet->msgId, 0xFFFFFFFF, 'MsgID');

        $fields = [
            $packet->clientId,
            $packet->deviceId,
            $packet->sessionId,
            $packet->messageId,
            $packet->traceId,
            $packet->token,
        ];
        foreach ($fields as $field) {
            if (strlen($field) > 0xFFFF) {
                throw new ProtocolException(
                    ProtocolException::FIELD_TOO_LARGE,
                    'string field exceeds 65535 bytes',
                );
            }
        }

        $bodyLength = strlen($packet->body);
        if ($bodyLength > 0xFFFFFFFF) {
            throw new ProtocolException(
                ProtocolException::BODY_TOO_LARGE,
                'body exceeds 4294967295 bytes',
            );
        }

        $encoded = pack('n', Packet::MAGIC)
            . chr(Packet::VERSION)
            . pack('nN', $packet->flags, $packet->msgId)
            . Integer64::encodeUnsigned($packet->sequence)
            . Integer64::encodeSigned($packet->timestamp);
        foreach ($fields as $field) {
            $encoded .= pack('n', strlen($field));
        }
        $encoded .= pack('N', $bodyLength);
        foreach ($fields as $field) {
            $encoded .= $field;
        }
        return $encoded . $packet->body;
    }

    public static function decode(string $data, int $maxBodySize = Packet::DEFAULT_MAX_BODY_SIZE): Packet
    {
        if ($maxBodySize < 0) {
            throw new ProtocolException(ProtocolException::BODY_TOO_LARGE, 'maximum body size cannot be negative');
        }
        $actualLength = strlen($data);
        if ($actualLength < Packet::FIXED_HEADER_SIZE) {
            throw new ProtocolException(
                ProtocolException::PACKET_TOO_SHORT,
                "packet has {$actualLength} bytes; expected at least " . Packet::FIXED_HEADER_SIZE,
            );
        }

        $magic = self::uint16($data, 0);
        if ($magic !== Packet::MAGIC) {
            throw new ProtocolException(ProtocolException::INVALID_MAGIC, 'invalid packet magic');
        }
        $version = ord($data[2]);
        if ($version !== Packet::VERSION) {
            throw new ProtocolException(
                ProtocolException::UNSUPPORTED_VERSION,
                "unsupported packet version: {$version}",
            );
        }

        $flags = self::uint16($data, 3);
        $msgId = self::uint32($data, 5);
        $sequence = Integer64::decodeUnsigned(substr($data, 9, 8));
        $timestamp = Integer64::decodeSigned(substr($data, 17, 8));

        $offset = 25;
        $fieldLengths = [];
        for ($index = 0; $index < 6; $index++) {
            $fieldLengths[] = self::uint16($data, $offset);
            $offset += 2;
        }
        $bodyLength = self::uint32($data, $offset);
        if ($bodyLength > $maxBodySize) {
            throw new ProtocolException(
                ProtocolException::BODY_TOO_LARGE,
                "body has {$bodyLength} bytes; maximum is {$maxBodySize}",
            );
        }

        $expectedLength = Packet::FIXED_HEADER_SIZE + array_sum($fieldLengths) + $bodyLength;
        if ($expectedLength !== $actualLength) {
            throw new ProtocolException(
                ProtocolException::LENGTH_MISMATCH,
                "packet length is {$actualLength}; expected {$expectedLength}",
            );
        }

        $cursor = Packet::FIXED_HEADER_SIZE;
        $fields = [];
        foreach ($fieldLengths as $fieldLength) {
            $fields[] = substr($data, $cursor, $fieldLength);
            $cursor += $fieldLength;
        }
        $body = substr($data, $cursor, $bodyLength);

        return new Packet(
            msgId: $msgId,
            body: $body,
            version: $version,
            flags: $flags,
            sequence: $sequence,
            timestamp: $timestamp,
            clientId: $fields[0],
            deviceId: $fields[1],
            sessionId: $fields[2],
            messageId: $fields[3],
            traceId: $fields[4],
            token: $fields[5],
        );
    }

    private static function uint16(string $data, int $offset): int
    {
        /** @var array{value: int} $decoded */
        $decoded = unpack('nvalue', $data, $offset);
        return $decoded['value'];
    }

    private static function uint32(string $data, int $offset): int
    {
        /** @var array{value: int} $decoded */
        $decoded = unpack('Nvalue', $data, $offset);
        return $decoded['value'];
    }

    private static function requireUnsigned(int $value, int $maximum, string $name): void
    {
        if ($value < 0 || $value > $maximum) {
            throw new ProtocolException(
                ProtocolException::INVALID_INTEGER,
                "{$name} is out of range: {$value}",
            );
        }
    }
}
