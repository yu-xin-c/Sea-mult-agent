import unittest

from common import classification_metrics


class ClassificationMetricsTest(unittest.TestCase):
    def test_unknown_prediction_counts_as_false_negative(self) -> None:
        metrics = classification_metrics(
            ["Code_Execution", "General"],
            ["__INVALID__", "General"],
        )

        code_metrics = metrics["per_class"]["Code_Execution"]
        self.assertEqual(metrics["unknown_predictions"], 1)
        self.assertEqual(metrics["unknown_by_expected"]["Code_Execution"], 1)
        self.assertEqual(code_metrics["support"], 1)
        self.assertEqual(code_metrics["recall"], 0.0)
        self.assertLess(metrics["macro_f1"], 1.0)


if __name__ == "__main__":
    unittest.main()
