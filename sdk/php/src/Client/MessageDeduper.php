<?php

declare(strict_types=1);

namespace ZCourier\Client;

final class MessageDeduper
{
    /** @var array<string, true> Oldest entry is stored first. */
    private array $entries = [];

    public function __construct(private readonly int $capacity)
    {
    }

    public function contains(string $messageId): bool
    {
        if (!array_key_exists($messageId, $this->entries)) {
            return false;
        }
        unset($this->entries[$messageId]);
        $this->entries[$messageId] = true;
        return true;
    }

    public function mark(string $messageId): void
    {
        unset($this->entries[$messageId]);
        $this->entries[$messageId] = true;
        if (count($this->entries) <= $this->capacity) {
            return;
        }
        $oldest = array_key_first($this->entries);
        if ($oldest !== null) {
            unset($this->entries[$oldest]);
        }
    }
}
