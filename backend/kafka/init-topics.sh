#!/bin/bash
set -e

BOOTSTRAP="kafka:29092"

echo "=== Creating PriceSpy Kafka topics ==="

kafka-topics --bootstrap-server "$BOOTSTRAP" --create --if-not-exists --topic pricespy.scrape.requested --partitions 3 --replication-factor 1
echo "✓ pricespy.scrape.requested"

kafka-topics --bootstrap-server "$BOOTSTRAP" --create --if-not-exists --topic pricespy.price.scraped --partitions 3 --replication-factor 1
echo "✓ pricespy.price.scraped"

kafka-topics --bootstrap-server "$BOOTSTRAP" --create --if-not-exists --topic pricespy.price.stored --partitions 3 --replication-factor 1
echo "✓ pricespy.price.stored"

echo ""
echo "=== All topics ==="
kafka-topics --bootstrap-server "$BOOTSTRAP" --list
echo "=== Done ==="
