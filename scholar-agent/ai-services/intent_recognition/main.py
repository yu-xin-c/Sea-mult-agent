from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
import uvicorn
# import torch
# from transformers import BertTokenizer, BertForSequenceClassification

app = FastAPI(title="Intent Recognition Service")

# Mock Model loading
# tokenizer = BertTokenizer.from_pretrained('bert-base-chinese')
# model = BertForSequenceClassification.from_pretrained('./models/fine_tuned_bert')

class IntentRequest(BaseModel):
    text: str

class IntentResponse(BaseModel):
    intent_type: str
    confidence: float
    entities: dict

@app.post("/predict", response_model=IntentResponse)
async def predict_intent(request: IntentRequest):
    """
    Predicts the intent of a given natural language task description.
    """
    text = request.text
    
    # In a real scenario, we would run inference here:
    # inputs = tokenizer(text, return_tensors="pt")
    # outputs = model(**inputs)
    # prediction = torch.argmax(outputs.logits, dim=-1)
    
    # Mocking the inference logic based on keywords
    intent_type = "General_Query"
    entities = {}
    
    if "代码" in text or "跑" in text or "运行" in text:
        intent_type = "Code_Execution"
    elif "复现" in text or "baseline" in text.lower():
        intent_type = "Paper_Reproduction"
    elif "综述" in text or "检索" in text or "论文" in text:
        intent_type = "Literature_Review"
    elif "数据" in text or "分析" in text or "图表" in text:
        intent_type = "Data_Analysis"
    elif "评估" in text or "对比" in text or "AB实验" in text or "A/B" in text or "选型" in text:
        intent_type = "Framework_Evaluation"
        
    # Mock entity extraction (NER)
    if "RAG" in text.upper():
        entities["topic"] = "RAG"
    
    return IntentResponse(
        intent_type=intent_type,
        confidence=0.95,
        entities=entities
    )

@app.get("/health")
def health_check():
    return {"status": "ok"}

if __name__ == "__main__":
    uvicorn.run(app, host="0.0.0.0", port=8000)
