<?php

declare(strict_types=1);

namespace ZCourier\Protocol;

use ZCourier\Exception\ProtocolException;

final class FrameParser
{
    private string $buffer = '';

    public function __construct(
        private readonly int $maxPayloadSize = FrameCodec::DEFAULT_MAX_PAYLOAD_SIZE,
        private readonly int $maxBodySize = Packet::DEFAULT_MAX_BODY_SIZE,
    ) {
        if ($maxPayloadSize < 0 || $maxBodySize < 0) {
            throw new ProtocolException(
                ProtocolException::FRAME_TOO_LARGE,
                'parser limits cannot be negative',
            );
        }
    }

    /** @return list<Packet> */
    public function push(string $chunk): array
    {
        $this->buffer .= $chunk;
        $packets = [];
        while (strlen($this->buffer) >= FrameCodec::HEADER_SIZE) {
            $payloadLength = FrameCodec::payloadLength($this->buffer);
            $limit = $this->maxPayloadSize === 0
                ? FrameCodec::DEFAULT_MAX_PAYLOAD_SIZE
                : $this->maxPayloadSize;
            if ($payloadLength > $limit) {
                throw new ProtocolException(
                    ProtocolException::FRAME_TOO_LARGE,
                    "frame payload has {$payloadLength} bytes; maximum is {$limit}",
                );
            }

            $frameLength = FrameCodec::HEADER_SIZE + $payloadLength;
            if (strlen($this->buffer) < $frameLength) {
                break;
            }
            $frame = substr($this->buffer, 0, $frameLength);
            $this->buffer = substr($this->buffer, $frameLength);
            $packets[] = FrameCodec::decode($frame, $limit, $this->maxBodySize);
        }
        return $packets;
    }

    public function bufferedBytes(): int
    {
        return strlen($this->buffer);
    }

    public function reset(): void
    {
        $this->buffer = '';
    }
}
